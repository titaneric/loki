package metric

import (
	"encoding/base64"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-kit/log"
	"github.com/grafana/dskit/backoff"
	"github.com/prometheus/common/config"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/grafana/loki/v3/pkg/logproto"
	"github.com/grafana/loki/v3/pkg/util"
)

const (
	testTenant   = "test1"
	testUsername = "user"
	testPassword = "secret"
	LogEntry     = "%s %s\n"
)

func Test_Push(t *testing.T) {
	lbls := labels.New(labels.Label{Name: "test", Value: "test"})

	// create dummy loki server
	responses := make(chan response, 1) // buffered not to block the response handler
	backoff := backoff.Config{
		MinBackoff: 300 * time.Millisecond,
		MaxBackoff: 5 * time.Minute,
		MaxRetries: 10,
	}

	// mock loki server
	mock := httptest.NewServer(createServerHandler(responses))
	require.NotNil(t, mock)
	defer mock.Close()

	// without TLS
	push, err := NewPush(
		mock.Listener.Addr().String(),
		"test1",
		2*time.Second,
		config.DefaultHTTPClientConfig,
		"", "",
		false,
		&backoff,
		log.NewNopLogger(),
	)
	require.NoError(t, err)
	ts, payload := testPayload()
	push.WriteEntry(ts, payload, lbls)
	resp := <-responses
	assertResponse(t, resp, false, labelSet("test", "test"), ts, payload)

	// with basic Auth
	push, err = NewPush(
		mock.Listener.Addr().String(),
		"test1",
		2*time.Second,
		config.DefaultHTTPClientConfig,
		"user", "secret",
		false,
		&backoff,
		log.NewNopLogger(),
	)
	require.NoError(t, err)
	ts, payload = testPayload()
	push.WriteEntry(ts, payload, lbls)
	resp = <-responses
	assertResponse(t, resp, true, labelSet("test", "test"), ts, payload)
}

// Test helpers

func assertResponse(t *testing.T, resp response, testAuth bool, labels labels.Labels, ts time.Time, payload string) {
	t.Helper()

	// assert metadata
	assert.Equal(t, testTenant, resp.tenantID)

	var expUser, expPass string

	if testAuth {
		expUser = testUsername
		expPass = testPassword
	}

	assert.Equal(t, expUser, resp.username)
	assert.Equal(t, expPass, resp.password)
	assert.Equal(t, defaultContentType, resp.contentType)
	assert.Equal(t, defaultUserAgent, resp.userAgent)

	// assert stream labels
	require.Len(t, resp.pushReq.Streams, 1)
	assert.Equal(t, labels.String(), resp.pushReq.Streams[0].Labels)
	assert.Equal(t, labels.Hash(), resp.pushReq.Streams[0].Hash)

	// assert log entry
	require.Len(t, resp.pushReq.Streams, 1)
	require.Len(t, resp.pushReq.Streams[0].Entries, 1)
	assert.Equal(t, payload, resp.pushReq.Streams[0].Entries[0].Line)
	assert.Equal(t, ts, resp.pushReq.Streams[0].Entries[0].Timestamp)
}

type response struct {
	tenantID           string
	pushReq            logproto.PushRequest
	contentType        string
	userAgent          string
	username, password string
}

func createServerHandler(responses chan response) http.HandlerFunc {
	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		// Parse the request
		var pushReq logproto.PushRequest
		if err := util.ParseProtoReader(req.Context(), req.Body, int(req.ContentLength), math.MaxInt32, &pushReq, util.RawSnappy); err != nil {
			rw.WriteHeader(500)
			return
		}

		var username, password string

		basicAuth := req.Header.Get("Authorization")
		if basicAuth != "" {
			encoded := strings.TrimPrefix(basicAuth, "Basic ") // now we have just encoded `username:password`
			decoded, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				rw.WriteHeader(500)
				return
			}
			toks := strings.FieldsFunc(string(decoded), func(r rune) bool {
				return r == ':'
			})
			username, password = toks[0], toks[1]
		}

		responses <- response{
			tenantID:    req.Header.Get("X-Scope-OrgID"),
			contentType: req.Header.Get("Content-Type"),
			userAgent:   req.Header.Get("User-Agent"),
			username:    username,
			password:    password,
			pushReq:     pushReq,
		}

		rw.WriteHeader(http.StatusOK)
	})
}

func labelSet(keyVals ...string) labels.Labels {
	if len(keyVals)%2 != 0 {
		panic("not matching key-value pairs")
	}

	lbls := labels.Labels{}

	for i := 0; i < len(keyVals)-1; i += 2 {
		lbls = append(lbls, labels.Label{Name: keyVals[i], Value: keyVals[i+1]})
	}

	return lbls
}

func testPayload() (time.Time, string) {
	ts := time.Now().UTC()
	payload := fmt.Sprintf(LogEntry, fmt.Sprint(ts.UnixNano()), "pppppp")

	return ts, payload
}
