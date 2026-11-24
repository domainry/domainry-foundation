package telemetry

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

var errOTLPAuthorizationRequiresHTTPS = errors.New("OTLP authorization requires a verified HTTPS endpoint")
var errInvalidOTLPAuthorization = errors.New("TELEMETRY_AUTHORIZATION must be a Basic or Bearer header")
var otlpAuthorizationPattern = regexp.MustCompile(`^(Basic [A-Za-z0-9+/]+={0,2}|Bearer [A-Za-z0-9._~+/-]+=*)$`)

type authenticatedOTLPTransport struct {
	*http.Transport
	authorization string
}

func (transport authenticatedOTLPTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Scheme != "https" {
		return nil, errOTLPAuthorizationRequiresHTTPS
	}
	if transport.authorization != "" {
		request = request.Clone(request.Context())
		request.Header.Set("Authorization", transport.authorization)
	}
	response, err := transport.Transport.RoundTrip(request)
	if err == nil && response.StatusCode >= http.StatusMultipleChoices {
		// The exporter logs failure bodies and status text supplied by the receiver.
		_ = response.Body.Close()
		response.Body = http.NoBody
		response.ContentLength = 0
		response.Status = fmt.Sprintf("%d %s", response.StatusCode, http.StatusText(response.StatusCode))
		// Even parsing an invalid redirect Location can include that value in an error.
		response.Header.Del("Location")
	}
	return response, err
}

func hasOTLPAuthorization(headers map[string]string) bool {
	if len(headers) == 0 {
		headers = parseEnvironmentHeaders(firstEnvironmentValue("OTEL_EXPORTER_OTLP_TRACES_HEADERS", "OTEL_EXPORTER_OTLP_HEADERS"))
	}
	for key := range headers {
		if strings.EqualFold(key, "Authorization") {
			return true
		}
	}
	return false
}

func authenticatedOTLPHTTPClient(config Config) (*http.Client, error) {
	if config.Authorization != "" && !otlpAuthorizationPattern.MatchString(config.Authorization) {
		return nil, errInvalidOTLPAuthorization
	}
	endpoint := strings.TrimSpace(config.Endpoint)
	if endpoint == "" {
		endpoint = firstEnvironmentValue("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_ENDPOINT")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || config.Insecure ||
		environmentBool("OTEL_EXPORTER_OTLP_TRACES_INSECURE", "OTEL_EXPORTER_OTLP_INSECURE") {
		return nil, errOTLPAuthorizationRequiresHTTPS
	}
	// WithHTTPClient owns transport configuration, so retain the exporter CA
	// and optional client certificate settings while prohibiting redirects.
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if file := firstEnvironmentValue("OTEL_EXPORTER_OTLP_TRACES_CERTIFICATE", "OTEL_EXPORTER_OTLP_CERTIFICATE"); file != "" {
		ca, err := os.ReadFile(file)
		roots := x509.NewCertPool()
		if err != nil || !roots.AppendCertsFromPEM(ca) {
			return nil, errors.New("cannot load OTLP certificate authority")
		}
		tlsConfig.RootCAs = roots
	}
	cert, key := firstEnvironmentValue("OTEL_EXPORTER_OTLP_TRACES_CLIENT_CERTIFICATE"), firstEnvironmentValue("OTEL_EXPORTER_OTLP_TRACES_CLIENT_KEY")
	if cert == "" && key == "" {
		cert, key = firstEnvironmentValue("OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE"), firstEnvironmentValue("OTEL_EXPORTER_OTLP_CLIENT_KEY")
	}
	if cert != "" || key != "" {
		pair, err := tls.LoadX509KeyPair(cert, key)
		if err != nil {
			return nil, errors.New("cannot load OTLP client certificate and key")
		}
		tlsConfig.Certificates = []tls.Certificate{pair}
	}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment, ForceAttemptHTTP2: true, TLSClientConfig: tlsConfig,
		TLSHandshakeTimeout: 10 * time.Second, IdleConnTimeout: 90 * time.Second,
	}
	timeout := config.ExportTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &http.Client{
		Transport: authenticatedOTLPTransport{Transport: transport, authorization: config.Authorization}, Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}
