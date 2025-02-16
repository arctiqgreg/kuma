package generate

import (
	"strings"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"

	kumactl_cmd "github.com/kumahq/kuma/app/kumactl/pkg/cmd"
)

type generateDataplaneTokenContext struct {
	*kumactl_cmd.RootContext

	args struct {
		dataplane string
		tags      map[string]string
	}
}

func NewGenerateDataplaneTokenCmd(pctx *kumactl_cmd.RootContext) *cobra.Command {
	ctx := &generateDataplaneTokenContext{RootContext: pctx}
	cmd := &cobra.Command{
		Use:   "dataplane-token",
		Short: "Generate Dataplane Token",
		Long:  `Generate Dataplane Token that is used to prove Dataplane identity.`,
		Example: `
Generate token bound by name and mesh
$ kumactl generate dataplane-token --mesh demo --dataplane demo-01

Generate token bound by mesh
$ kumactl generate dataplane-token --mesh demo

Generate token bound by tag
$ kumactl generate dataplane-token --mesh demo --tag kuma.io/service=web,web-api
`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := pctx.CurrentDataplaneTokenClient()
			if err != nil {
				return errors.Wrap(err, "failed to create dataplane token client")
			}

			tags := map[string][]string{}
			for k, v := range ctx.args.tags {
				tags[k] = strings.Split(v, ",")
			}
			token, err := client.Generate(ctx.args.dataplane, pctx.Args.Mesh, tags)
			if err != nil {
				return errors.Wrap(err, "failed to generate a dataplane token")
			}
			_, err = cmd.OutOrStdout().Write([]byte(token))
			return err
		},
	}
	cmd.Flags().StringVar(&ctx.args.dataplane, "dataplane", "", "name of the Dataplane")
	cmd.Flags().StringToStringVar(&ctx.args.tags, "tag", nil, "required tag values for dataplane (split values by comma to provide multiple values)")
	return cmd
}
