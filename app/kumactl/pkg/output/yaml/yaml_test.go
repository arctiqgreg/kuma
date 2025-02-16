package yaml_test

import (
	"bytes"
	"io/ioutil"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"

	"github.com/kumahq/kuma/app/kumactl/pkg/output"
	"github.com/kumahq/kuma/app/kumactl/pkg/output/yaml"

	mesh_proto "github.com/kumahq/kuma/api/mesh/v1alpha1"
	mesh_core "github.com/kumahq/kuma/pkg/core/resources/apis/mesh"
	core_rest "github.com/kumahq/kuma/pkg/core/resources/model/rest"
)

var _ = Describe("printer", func() {

	var printer output.Printer
	var buf *bytes.Buffer
	t1, _ := time.Parse(time.RFC3339, "2018-07-17T16:05:36.995+00:00")
	t2, _ := time.Parse(time.RFC3339, "2019-07-17T16:05:36.995+00:00")
	BeforeEach(func() {
		printer = yaml.NewPrinter()
		buf = &bytes.Buffer{}
	})

	type testCase struct {
		obj        interface{}
		goldenFile string
	}

	DescribeTable("should produce formatted output",
		func(given testCase) {
			// when
			err := printer.Print(given.obj, buf)
			// then
			Expect(err).ToNot(HaveOccurred())

			// when
			expected, err := ioutil.ReadFile(filepath.Join("testdata", given.goldenFile))
			// then
			Expect(err).ToNot(HaveOccurred())

			// and
			Expect(buf.String()).To(Equal(string(expected)))
		},
		Entry("format `nil` value", testCase{
			obj:        nil,
			goldenFile: "nil.golden.yaml",
		}),
		Entry("format response from Kuma REST API", testCase{
			obj: &core_rest.Resource{
				Meta: core_rest.ResourceMeta{
					Type:             string(mesh_core.MeshType),
					Name:             "demo",
					CreationTime:     t1,
					ModificationTime: t2,
				},
				Spec: &mesh_proto.Mesh{
					Mtls: &mesh_proto.Mesh_Mtls{
						EnabledBackend: "builtin-1",
						Backends: []*mesh_proto.CertificateAuthorityBackend{
							{
								Name: "builtin-1",
								Type: "builtin",
							},
						},
					},
				},
			},
			goldenFile: "mesh.golden.yaml",
		}),
	)
})
