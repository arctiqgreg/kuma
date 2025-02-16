package listeners

import (
	net_url "net/url"

	structpb "github.com/golang/protobuf/ptypes/struct"
	"github.com/pkg/errors"

	mesh_proto "github.com/kumahq/kuma/api/mesh/v1alpha1"
	"github.com/kumahq/kuma/pkg/util/proto"
	"github.com/kumahq/kuma/pkg/xds/envoy/names"

	envoy_listener "github.com/envoyproxy/go-control-plane/envoy/api/v2/listener"
	envoy_hcm "github.com/envoyproxy/go-control-plane/envoy/config/filter/network/http_connection_manager/v2"
	envoy_trace "github.com/envoyproxy/go-control-plane/envoy/config/trace/v2"
	envoy_type "github.com/envoyproxy/go-control-plane/envoy/type"
)

func Tracing(backend *mesh_proto.TracingBackend) FilterChainBuilderOpt {
	return FilterChainBuilderOptFunc(func(config *FilterChainBuilderConfig) {
		config.Add(&TracingConfigurer{
			backend: backend,
		})
	})
}

type TracingConfigurer struct {
	backend *mesh_proto.TracingBackend
}

func (c *TracingConfigurer) Configure(filterChain *envoy_listener.FilterChain) error {
	if c.backend == nil {
		return nil
	}

	return UpdateHTTPConnectionManager(filterChain, func(hcm *envoy_hcm.HttpConnectionManager) error {
		hcm.Tracing = &envoy_hcm.HttpConnectionManager_Tracing{}
		if c.backend.Sampling != nil {
			hcm.Tracing.OverallSampling = &envoy_type.Percent{
				Value: c.backend.Sampling.Value,
			}
		}
		switch c.backend.Type {
		case mesh_proto.TracingZipkinType:
			tracing, err := zipkinConfig(c.backend.Conf, c.backend.Name)
			if err != nil {
				return err
			}
			hcm.Tracing.Provider = tracing
		}
		return nil
	})
}

func zipkinConfig(cfgStr *structpb.Struct, backendName string) (*envoy_trace.Tracing_Http, error) {
	cfg := mesh_proto.ZipkinTracingBackendConfig{}
	if err := proto.ToTyped(cfgStr, &cfg); err != nil {
		return nil, errors.Wrap(err, "could not convert backend")
	}
	url, err := net_url.ParseRequestURI(cfg.Url)
	if err != nil {
		return nil, errors.Wrap(err, "invalid URL of Zipkin")
	}

	zipkinConfig := envoy_trace.ZipkinConfig{
		CollectorCluster:         names.GetTracingClusterName(backendName),
		CollectorEndpoint:        url.Path,
		TraceId_128Bit:           cfg.TraceId128Bit,
		CollectorEndpointVersion: apiVersion(&cfg, url),
	}
	zipkinConfigAny, err := proto.MarshalAnyDeterministic(&zipkinConfig)
	if err != nil {
		return nil, err
	}
	tracingConfig := &envoy_trace.Tracing_Http{
		Name: "envoy.zipkin",
		ConfigType: &envoy_trace.Tracing_Http_TypedConfig{
			TypedConfig: zipkinConfigAny,
		},
	}
	return tracingConfig, nil
}

func apiVersion(zipkin *mesh_proto.ZipkinTracingBackendConfig, url *net_url.URL) envoy_trace.ZipkinConfig_CollectorEndpointVersion {
	if zipkin.ApiVersion == "" { // try to infer it from the URL
		if url.Path == "/api/v1/spans" {
			return envoy_trace.ZipkinConfig_HTTP_JSON_V1
		} else if url.Path == "/api/v2/spans" {
			return envoy_trace.ZipkinConfig_HTTP_JSON
		}
	} else {
		switch zipkin.ApiVersion {
		case "httpJsonV1":
			return envoy_trace.ZipkinConfig_HTTP_JSON_V1
		case "httpJson":
			return envoy_trace.ZipkinConfig_HTTP_JSON
		case "httpProto":
			return envoy_trace.ZipkinConfig_HTTP_PROTO
		}
	}
	return envoy_trace.ZipkinConfig_HTTP_JSON
}
