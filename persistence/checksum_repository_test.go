package persistence

import (
	"github.com/astaxie/beego/orm"
	"github.com/cloudsonic/sonic-server/model"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("ChecksumRepository", func() {
	var repo model.ChecksumRepository

	BeforeEach(func() {
		repo = NewCheckSumRepository(orm.NewOrm())
		err := repo.SetData(map[string]string{
			"a": "AAA", "b": "BBB",
		})
		if err != nil {
			panic(err)
		}
	})

	It("can retrieve data", func() {
		sums, err := repo.GetData()
		Expect(err).To(BeNil())
		Expect(sums["b"]).To(Equal("BBB"))
	})

	It("persists data", func() {
		newRepo := NewCheckSumRepository(orm.NewOrm())
		sums, err := newRepo.GetData()
		Expect(err).To(BeNil())
		Expect(sums["b"]).To(Equal("BBB"))
	})
})