package admin_server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/emicklei/go-restful"
	"github.com/pkg/errors"
	"github.com/slok/go-http-metrics/metrics/prometheus"
	"github.com/slok/go-http-metrics/middleware"
	"go.uber.org/multierr"

	admin_server "github.com/kumahq/kuma/pkg/config/admin-server"
	config_core "github.com/kumahq/kuma/pkg/config/core"
	"github.com/kumahq/kuma/pkg/core"
	"github.com/kumahq/kuma/pkg/core/runtime"
	"github.com/kumahq/kuma/pkg/metrics"
	"github.com/kumahq/kuma/pkg/tokens/builtin"
	tokens_server "github.com/kumahq/kuma/pkg/tokens/builtin/server"
	util_prometheus "github.com/kumahq/kuma/pkg/util/prometheus"
)

var (
	log = core.Log.WithName("admin-server")
)

type AdminServer struct {
	cfg       admin_server.AdminServerConfig
	container *restful.Container
	metrics   metrics.Metrics
}

func (a *AdminServer) NeedLeaderElection() bool {
	return false
}

func NewAdminServer(cfg admin_server.AdminServerConfig, metrics metrics.Metrics, services ...*restful.WebService) *AdminServer {
	container := restful.NewContainer()
	for _, service := range services {
		container.Add(service)
	}

	promMiddleware := middleware.New(middleware.Config{
		Recorder: prometheus.NewRecorder(prometheus.Config{
			Registry: metrics,
			Prefix:   "admin_server",
		}),
	})
	container.Filter(util_prometheus.MetricsHandler("", promMiddleware))

	return &AdminServer{
		cfg:       cfg,
		container: container,
		metrics:   metrics,
	}
}

func (a *AdminServer) Start(stop <-chan struct{}) error {
	httpServer, httpErrChan := a.startHttpServer()

	var httpsServer *http.Server
	var httpsErrChan chan error
	if a.cfg.Public.Enabled {
		httpsServer, httpsErrChan = a.startHttpsServer()
	} else {
		httpsErrChan = make(chan error)
	}

	select {
	case <-stop:
		log.Info("stopping")
		var multiErr error
		if err := httpServer.Shutdown(context.Background()); err != nil {
			multiErr = multierr.Combine(err)
		}
		if httpsServer != nil {
			if err := httpsServer.Shutdown(context.Background()); err != nil {
				multiErr = multierr.Combine(err)
			}
		}
		return multiErr
	case err := <-httpErrChan:
		return err
	case err := <-httpsErrChan:
		return err
	}
}

func (a *AdminServer) startHttpServer() (*http.Server, chan error) {
	server := &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", a.cfg.Local.Port),
		Handler: a.container,
	}

	errChan := make(chan error)

	go func() {
		defer close(errChan)
		if err := server.ListenAndServe(); err != nil {
			if err != http.ErrServerClosed {
				log.Error(err, "http server terminated with an error")
				errChan <- err
				return
			}
		}
		log.Info("http server terminated normally")
	}()
	log.Info("starting server", "interface", "127.0.0.1", "port", a.cfg.Local.Port)
	return server, errChan
}

func (a *AdminServer) startHttpsServer() (*http.Server, chan error) {
	errChan := make(chan error)

	tlsConfig, err := requireClientCerts(a.cfg.Public.ClientCertsDir)
	if err != nil {
		errChan <- err
	}

	server := &http.Server{
		Addr:      fmt.Sprintf("%s:%d", a.cfg.Public.Interface, a.cfg.Public.Port),
		Handler:   a.container,
		TLSConfig: tlsConfig,
	}

	go func() {
		defer close(errChan)
		if err := server.ListenAndServeTLS(a.cfg.Public.TlsCertFile, a.cfg.Public.TlsKeyFile); err != nil {
			if err != http.ErrServerClosed {
				log.Error(err, "https server terminated with an error")
				errChan <- err
				return
			}
		}
		log.Info("https server terminated normally")
	}()
	log.Info("starting server", "interface", a.cfg.Public.Interface, "port", a.cfg.Public.Port, "tls", true)
	return server, errChan
}

func requireClientCerts(certsDir string) (*tls.Config, error) {
	files, err := ioutil.ReadDir(certsDir)
	if err != nil {
		return nil, err
	}
	clientCertPool := x509.NewCertPool()
	for _, file := range files {
		if file.IsDir() {
			continue
		}
		if !strings.HasSuffix(file.Name(), ".pem") {
			continue
		}
		path := filepath.Join(certsDir, file.Name())
		caCert, err := ioutil.ReadFile(path)
		if err != nil {
			return nil, errors.Wrapf(err, "could not read certificate %s", path)
		}
		clientCertPool.AppendCertsFromPEM(caCert)
	}
	tlsConfig := &tls.Config{
		ClientCAs:  clientCertPool,
		ClientAuth: tls.RequireAndVerifyClientCert,
	}
	return tlsConfig, nil
}

func SetupServer(rt runtime.Runtime) error {
	var webservices []*restful.WebService

	ws := new(restful.WebService).
		Consumes(restful.MIME_JSON).
		Produces(restful.MIME_JSON)
	endpoints := secretsEndpoints{rt.ResourceManager()}
	endpoints.addFindEndpoint(ws)
	endpoints.addListEndpoint(ws)
	if !rt.Config().ApiServer.ReadOnly {
		endpoints.addDeleteEndpoint(ws)
		endpoints.addCreateOrUpdateEndpoint(ws)
	}
	webservices = append(webservices, ws)

	ws, err := dataplaneTokenWs(rt)
	if err != nil {
		return err
	}
	if ws != nil {
		webservices = append(webservices, ws)
	}

	srv := NewAdminServer(*rt.Config().AdminServer, rt.Metrics(), webservices...)
	return rt.Add(srv)
}

func dataplaneTokenWs(rt runtime.Runtime) (*restful.WebService, error) {
	if !rt.Config().AdminServer.Apis.DataplaneToken.Enabled {
		log.Info("Dataplane Token Webservice is disabled. Dataplane Tokens won't be verified.")
		return nil, nil
	}

	start := true
	switch rt.Config().Mode {
	case config_core.Standalone, config_core.Remote:
		// we still want to generate tokens on Universal even when Global CP is down, so we can scale up and down DPs
		start = rt.Config().Environment == config_core.UniversalEnvironment
	case config_core.Global:
		// the flow may require to generate tokens for Universal's Remote on K8S Global
		start = true
	}

	if start {
		generator, err := builtin.NewDataplaneTokenIssuer(rt)
		if err != nil {
			return nil, err
		}
		return tokens_server.NewWebservice(generator), nil
	}
	return nil, nil
}
