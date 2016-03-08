package persistence

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/deluan/gosonic/domain"
	"github.com/deluan/gosonic/utils"
	"github.com/siddontang/ledisdb/ledis"
)

type ledisRepository struct {
	table         string
	entityType    reflect.Type
	fieldNames    []string
	parentTable   string
	parentIdField string
}

func (r *ledisRepository) init(table string, entity interface{}) {
	r.table = table
	r.entityType = reflect.TypeOf(entity).Elem()

	h, _ := utils.ToMap(entity)
	r.fieldNames = make([]string, len(h))
	i := 0
	for k := range h {
		r.fieldNames[i] = k
		i++
	}
	r.parentTable, r.parentIdField, _ = r.getParent(entity)
}

// TODO Use annotations to specify fields to be used
func (r *ledisRepository) NewId(fields ...string) string {
	s := fmt.Sprintf("%s\\%s", strings.ToUpper(r.table), strings.Join(fields, ""))
	return fmt.Sprintf("%x", md5.Sum([]byte(s)))
}

func (r *ledisRepository) CountAll() (int64, error) {
	size, err := db().ZCard([]byte(r.table + "s:all"))
	return size, err
}

func (r *ledisRepository) GetAllIds() (map[string]bool, error) {
	m := make(map[string]bool)
	pairs, err := db().ZRange([]byte(r.table+"s:all"), 0, -1)
	if err != nil {
		return m, err
	}
	for _, p := range pairs {
		m[string(p.Member)] = true
	}
	return m, err
}

func (r *ledisRepository) DeleteAll(ids map[string]bool) error {
	allKey := r.table + "s:all"
	keys := make([][]byte, len(ids))

	i := 0
	for id, _ := range ids {
		// Delete from parent:parentId:table (ZSet)
		if r.parentTable != "" {
			parentKey := []byte(fmt.Sprintf("%s:%s:%s", r.table, id, r.parentIdField))
			pid, err := db().Get(parentKey)
			var parentId string
			if err := json.Unmarshal(pid, &parentId); err != nil {
				return err
			}
			if err != nil {
				return err
			}
			parentKey = []byte(fmt.Sprintf("%s:%s:%ss", r.parentTable, parentId, r.table))
			if _, err := db().ZRem(parentKey, []byte(id)); err != nil {
				return err
			}
		}

		// Delete record table:id:* (KV)
		if err := r.deleteRecord(id); err != nil {
			return err
		}
		keys[i] = []byte(id)

		i++
	}

	// Delete from table:all (ZSet)
	_, err := db().ZRem([]byte(allKey), keys...)

	return err
}

func (r *ledisRepository) deleteRecord(id string) error {
	keys := r.getFieldKeys(id)
	_, err := db().Del(keys...)
	return err
}

func (r *ledisRepository) Exists(id string) (bool, error) {
	res, _ := db().ZScore([]byte(r.table+"s:all"), []byte(id))
	return res != ledis.InvalidScore, nil
}

func (r *ledisRepository) saveOrUpdate(id string, entity interface{}) error {
	recordPrefix := fmt.Sprintf("%s:%s:", r.table, id)
	allKey := r.table + "s:all"

	h, err := utils.ToMap(entity)
	if err != nil {
		return err
	}
	for f, v := range h {
		key := recordPrefix + f
		value, _ := json.Marshal(v)
		if err := db().Set([]byte(key), value); err != nil {
			return err
		}

	}

	sid := ledis.ScorePair{0, []byte(id)}
	if _, err = db().ZAdd([]byte(allKey), sid); err != nil {
		return err
	}

	if parentCollectionKey := r.getParentRelationKey(entity); parentCollectionKey != "" {
		_, err = db().ZAdd([]byte(parentCollectionKey), sid)
	}
	return nil
}

func (r *ledisRepository) getParentRelationKey(entity interface{}) string {
	parentId := r.getParentId(entity)
	if parentId != "" {
		return fmt.Sprintf("%s:%s:%ss", r.parentTable, parentId, r.table)
	}
	return ""
}

// TODO Optimize
func (r *ledisRepository) getParent(entity interface{}) (table string, idField string, id string) {
	dt := reflect.TypeOf(entity).Elem()
	for i := 0; i < dt.NumField(); i++ {
		f := dt.Field(i)
		table = f.Tag.Get("parent")
		if table != "" {
			idField = f.Name
			dv := reflect.ValueOf(entity).Elem()
			id = dv.FieldByName(f.Name).String()
			return
		}
	}
	return
}

func (r *ledisRepository) getParentId(entity interface{}) string {
	if r.parentTable != "" {
		dv := reflect.ValueOf(entity).Elem()
		return dv.FieldByName(r.parentIdField).String()
	}
	return ""
}

func (r *ledisRepository) getFieldKeys(id string) [][]byte {
	recordPrefix := fmt.Sprintf("%s:%s:", r.table, id)
	var fieldKeys = make([][]byte, len(r.fieldNames))
	for i, n := range r.fieldNames {
		fieldKeys[i] = []byte(recordPrefix + n)
	}
	return fieldKeys
}

func (r *ledisRepository) newInstance() interface{} {
	return reflect.New(r.entityType).Interface()
}

func (r *ledisRepository) readEntity(id string) (interface{}, error) {
	entity := r.newInstance()

	fieldKeys := r.getFieldKeys(id)

	res, err := db().MGet(fieldKeys...)
	if err != nil {
		return nil, err
	}
	err = r.toEntity(res, entity)
	return entity, err
}

func (r *ledisRepository) toEntity(response [][]byte, entity interface{}) error {
	var record = make(map[string]interface{}, len(response))
	for i, v := range response {
		if len(v) > 0 {
			var value interface{}
			if err := json.Unmarshal(v, &value); err != nil {
				return err
			}
			record[string(r.fieldNames[i])] = value
		}
	}

	return utils.ToStruct(record, entity)
}

func (r *ledisRepository) loadAll(entities interface{}, qo ...domain.QueryOptions) error {
	setName := r.table + "s:all"
	return r.loadFromSet(setName, entities, qo...)
}

func (r *ledisRepository) loadChildren(parentTable string, parentId string, emptyEntityArray interface{}, qo ...domain.QueryOptions) error {
	setName := fmt.Sprintf("%s:%s:%ss", parentTable, parentId, r.table)
	return r.loadFromSet(setName, emptyEntityArray, qo...)
}

// TODO Optimize it! Probably very slow (and confusing!)
func (r *ledisRepository) loadFromSet(setName string, entities interface{}, qo ...domain.QueryOptions) error {
	o := domain.QueryOptions{}
	if len(qo) > 0 {
		o = qo[0]
	}

	reflected := reflect.ValueOf(entities).Elem()
	var sortKey []byte = nil
	if o.SortBy != "" {
		sortKey = []byte(fmt.Sprintf("%s:*:%s", r.table, o.SortBy))
	}
	response, err := db().XZSort([]byte(setName), o.Offset, o.Size, o.Alpha, o.Desc, sortKey, r.getFieldKeys("*"))
	if err != nil {
		return err
	}
	numFields := len(r.fieldNames)
	for i := 0; i < (len(response) / numFields); i++ {
		start := i * numFields
		entity := reflect.New(r.entityType).Interface()

		if err := r.toEntity(response[start:start+numFields], entity); err != nil {
			return err
		}
		reflected.Set(reflect.Append(reflected, reflect.ValueOf(entity).Elem()))
	}

	return nil

}