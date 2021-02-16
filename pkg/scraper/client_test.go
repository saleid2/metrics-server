package scraper

// https://golang.org/pkg/net/http/httptest/#Server.Client
// https://erikwinter.nl/articles/2020/unit-test-outbound-http-requests-in-golang/

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/metrics-server/pkg/utils"
)

var _ = Describe("Client", func() {
	var (
		mockRecorder     = &MockServerRecorder{Requests: []*http.Request{}}
		mockServer       = newMockServer(mockRecorder, newMockServerProcedure("", true, false), newMockServerProcedure("", false, true))
		mockServerURL, _ = url.Parse(mockServer.URL)
		proxyServerAddr  = net.JoinHostPort(mockServerURL.Hostname(), mockServerURL.Port())
		nodeAddr         = mockServerURL.Hostname()
		nodePort, _      = strconv.Atoi(mockServerURL.Port())
		httpClient       = mockServer.Client()
		addrResolver     = utils.NewPriorityNodeAddressResolver([]corev1.NodeAddressType{corev1.NodeInternalIP})
		kubeletClient    = makeKubelet(nodePort, httpClient, addrResolver, proxyServerAddr)
	)
	BeforeEach(func() {
		mockRecorder.Requests = []*http.Request{}
	})
	Context("when node doesn't have label to use proxy", func() {
		It("should connect to the node directly", func() {
			By("invoking GetSummary with a context timeout of 5 seconds")
			nodeWOProxy := makeNode("node1", "node1.somedomain", nodeAddr, true, false)

			timeoutCtx, doneWithWork := context.WithTimeout(context.Background(), 4*time.Second)
			_, err := kubeletClient.GetSummary(timeoutCtx, nodeWOProxy)
			doneWithWork()
			Expect(err).NotTo(HaveOccurred())

			By("ensuring only 1 request was received by the mock server")
			Expect(mockRecorder.Requests).Should(HaveLen(1))

			By("ensuring that request only contains parameter only_cpu_and_memory=true and not nodeIp nor nodePort")
			Expect(mockRecorder.Requests[0].URL.RawQuery).To(
				SatisfyAll(
					ContainSubstring("only_cpu_and_memory=true"),
					Not(ContainSubstring("nodeIp=")),
					Not(ContainSubstring("nodePort="))))
		})
	})
	Context("when node has label to use proxy", func() {
		It("should connect to the proxy instead of the node", func() {
			By("invoking GetSummary with a context timeout of 5 seconds")
			nodeWProxy := makeNode("node1", "node1.somedomain", nodeAddr, true, true)

			timeoutCtx, doneWithWork := context.WithTimeout(context.Background(), 4*time.Second)
			_, err := kubeletClient.GetSummary(timeoutCtx, nodeWProxy)
			doneWithWork()
			Expect(err).NotTo(HaveOccurred())

			By("ensuring only 1 request was received by the mock server")
			Expect(mockRecorder.Requests).Should(HaveLen(1))

			By("ensuring that request contains parameters only_cpu_and_memory, nodeIp and nodePort")
			Expect(mockRecorder.Requests[0].URL.RawQuery).To(
				SatisfyAll(
					ContainSubstring("only_cpu_and_memory=true"),
					ContainSubstring("nodeIp="),
					ContainSubstring("nodePort=")))

			By("ensuring that NodeIP is correct")
			Expect(mockRecorder.Requests[0].URL.RawQuery).To(ContainSubstring(fmt.Sprint("nodeIp=", nodeAddr)))

			By("ensuring that NodePort is correct")
			Expect(mockRecorder.Requests[0].URL.RawQuery).To(ContainSubstring(fmt.Sprint("nodePort=", nodePort)))
		})
	})
})

func makeKubelet(defaultPort int, client *http.Client, addrResolver utils.NodeAddressResolver, KubeletProxyServerAddress string) *kubeletClient {
	res := &kubeletClient{
		defaultPort:               defaultPort,
		useNodeStatusPort:         false,
		client:                    client,
		scheme:                    "http",
		addrResolver:              addrResolver,
		KubeletProxyServerAddress: KubeletProxyServerAddress,
		buffers: sync.Pool{
			New: func() interface{} {
				return new(bytes.Buffer)
			},
		},
	}

	return res
}

type MockServerResponse struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
}

type MockServerProcedure struct {
	URI        string
	HTTPMethod string
	Response   MockServerResponse
}

type MockServerRecorder struct {
	Requests []*http.Request
}

func newMockServer(recorder *MockServerRecorder, procedures ...MockServerProcedure) *httptest.Server {
	var handler http.Handler

	handler = http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r != nil {
				recorder.Requests = append(recorder.Requests, r)
			}

			for _, proc := range procedures {
				if proc.HTTPMethod == r.Method {
					headers := w.Header()
					for hkey, hvalue := range proc.Response.Headers {
						headers[hkey] = hvalue
					}
					code := proc.Response.StatusCode

					w.WriteHeader(code)
					w.Write(proc.Response.Body)
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)
			return
		})

	return httptest.NewServer(handler)
}

func newMockServerProcedure(url string, isSuccess bool, isProxied bool) MockServerProcedure {
	var statusCode int

	if isSuccess {
		statusCode = http.StatusOK
	} else {
		statusCode = http.StatusNotFound
	}

	res := MockServerProcedure{
		URI:        url,
		HTTPMethod: http.MethodGet,
		Response: MockServerResponse{
			StatusCode: statusCode,
			Body:       []byte(`{"result": "ok"}`),
		},
	}

	return res
}
