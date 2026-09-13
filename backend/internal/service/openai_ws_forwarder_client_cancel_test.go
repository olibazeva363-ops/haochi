package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestForwardOpenAIWSV2_ClientCancellationDrainsWithoutSyntheticFailure
// reproduces the HTTP/SSE ingress bug: the client cancels after partial output,
// while the upstream WS still has a terminal event available for usage billing.
func TestForwardOpenAIWSV2_ClientCancellationDrainsWithoutSyntheticFailure(t *testing.T) {
	result, writer, err := runOpenAIWSV2ClientCancellationTest(t, &openAIWSCancelSafeConn{openAIWSCaptureConn: &openAIWSCaptureConn{
		events: [][]byte{
			[]byte(`{"type":"response.created","response":{"id":"resp_cancel_1","model":"gpt-5.5"}}`),
			[]byte(`{"type":"response.output_text.delta","delta":"partial"}`),
			[]byte(`{"type":"response.output_item.done","item":{"id":"ig_cancel_1","type":"image_generation_call","result":"final-image","size":"1024x1024"}}`),
			[]byte(`{"type":"response.completed","response":{"id":"resp_cancel_1","model":"gpt-5.5","usage":{"input_tokens":3,"output_tokens":5}}}`),
		},
		readDelays: []time.Duration{0, 0, 50 * time.Millisecond, 0},
	}}, `{"model":"gpt-5.5","stream":true,"reasoning":{"effort":"high"},"tools":[{"type":"image_generation","model":"gpt-image-2","size":"2048x1152"}],"tool_choice":{"type":"image_generation"},"input":[{"type":"input_text","text":"draw a test image"}]}`)

	require.NoError(t, err, "client cancellation must not surface as an upstream failure")
	require.NotNil(t, result)
	require.True(t, result.ClientDisconnect)
	require.Equal(t, "resp_cancel_1", result.RequestID)
	require.Equal(t, 3, result.Usage.InputTokens)
	require.Equal(t, 5, result.Usage.OutputTokens)
	require.Equal(t, 1, result.ImageCount)
	require.Equal(t, []string{"1024x1024"}, result.ImageOutputSizes)
	require.Equal(t, "gpt-image-2", result.BillingModel)
	require.Equal(t, "2K", result.ImageSize)
	require.Equal(t, "2048x1152", result.ImageInputSize)
	require.NotNil(t, result.RequestedReasoningEffort)
	require.Equal(t, "high", *result.RequestedReasoningEffort)
	require.NotContains(t, writer.body.String(), "response.failed")
}

func TestForwardOpenAIWSV2_IncompleteClientDisconnectPreservesBillingMetadata(t *testing.T) {
	result, writer, err := runOpenAIWSV2ClientCancellationTest(t, &openAIWSCancelSafeConn{openAIWSCaptureConn: &openAIWSCaptureConn{
		events: [][]byte{
			[]byte(`{"type":"response.created","response":{"id":"resp_cancel_incomplete","model":"gpt-5.5"}}`),
			[]byte(`{"type":"response.output_text.delta","delta":"partial"}`),
			[]byte(`{"type":"response.output_item.done","item":{"id":"ig_cancel_incomplete","type":"image_generation_call","result":"final-image","size":"1024x1024"}}`),
		},
		readDelays: []time.Duration{0, 0, 50 * time.Millisecond},
	}}, `{"model":"gpt-5.5","stream":true,"reasoning":{"effort":"high"},"tools":[{"type":"image_generation","model":"gpt-image-2","size":"2048x1152"}],"tool_choice":{"type":"image_generation"},"input":[{"type":"input_text","text":"draw a test image"}]}`)

	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	require.NotNil(t, result)
	require.True(t, result.ClientDisconnect)
	require.Equal(t, "resp_cancel_incomplete", result.RequestID)
	require.Equal(t, 1, result.ImageCount)
	require.Equal(t, []string{"1024x1024"}, result.ImageOutputSizes)
	require.Equal(t, "gpt-image-2", result.BillingModel)
	require.Equal(t, "2K", result.ImageSize)
	require.Equal(t, "2048x1152", result.ImageInputSize)
	require.NotNil(t, result.RequestedReasoningEffort)
	require.Equal(t, "high", *result.RequestedReasoningEffort)
	require.NotContains(t, writer.body.String(), "response.failed")
}

func TestForwardOpenAIWSV2_IncompleteTextClientDisconnectPreservesBillingModel(t *testing.T) {
	result, writer, err := runOpenAIWSV2ClientCancellationTest(t, &openAIWSCancelSafeConn{openAIWSCaptureConn: &openAIWSCaptureConn{
		events: [][]byte{
			[]byte(`{"type":"response.created","response":{"id":"resp_text_incomplete","model":"gpt-5.5"}}`),
			[]byte(`{"type":"response.output_text.delta","delta":"partial"}`),
		},
		readDelays: []time.Duration{0, 0},
	}}, `{"model":"gpt-5.5","stream":true,"reasoning":{"effort":"high"},"input":[{"type":"input_text","text":"hello"}]}`)

	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	require.NotNil(t, result)
	require.True(t, result.ClientDisconnect)
	require.Equal(t, "gpt-5.5", result.BillingModel)
	require.Zero(t, result.ImageCount)
	require.Empty(t, result.ImageSize)
	require.Empty(t, result.ImageInputSize)
	require.NotContains(t, writer.body.String(), "response.failed")
}

// Cancel while the lease is waiting for the next event, rather than inside a
// downstream write. A real coder connection exercises the resident reader loop
// and catches either that loop or the forwarder closing the socket before drain.
func TestForwardOpenAIWSV2_ReaderLoopCancellationDrainsBillingMetadata(t *testing.T) {
	for _, terminal := range []bool{true, false} {
		name := "incomplete"
		if terminal {
			name = "complete"
		}
		t.Run(name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			releaseUpstream := make(chan struct{})
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(releaseUpstream) }) }
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := coderws.Accept(w, r, nil)
				if err != nil {
					t.Errorf("accept websocket: %v", err)
					return
				}
				defer func() { _ = conn.CloseNow() }()
				serverCtx, stop := context.WithTimeout(r.Context(), 5*time.Second)
				defer stop()
				if _, _, err := conn.Read(serverCtx); err != nil {
					t.Errorf("read response.create: %v", err)
					return
				}
				for _, event := range []string{
					`{"type":"response.created","response":{"id":"resp_reader_cancel","model":"gpt-5.5"}}`,
					`{"type":"response.output_text.delta","delta":"partial"}`,
				} {
					if err := conn.Write(serverCtx, coderws.MessageText, []byte(event)); err != nil {
						return
					}
				}
				select {
				case <-releaseUpstream:
				case <-serverCtx.Done():
					return
				}
				if err := conn.Write(serverCtx, coderws.MessageText, []byte(`{"type":"response.output_item.done","item":{"id":"ig_reader_cancel","type":"image_generation_call","result":"final-image","size":"1024x1024"}}`)); err != nil {
					return
				}
				if terminal {
					_ = conn.Write(serverCtx, coderws.MessageText, []byte(`{"type":"response.completed","response":{"id":"resp_reader_cancel","model":"gpt-5.5","usage":{"input_tokens":3,"output_tokens":5}}}`))
				}
			}))
			defer server.Close()
			defer release()

			cfg := &config.Config{}
			cfg.Security.URLAllowlist.Enabled = false
			cfg.Security.URLAllowlist.AllowInsecureHTTP = true
			cfg.Gateway.OpenAIWS.Enabled = true
			cfg.Gateway.OpenAIWS.APIKeyEnabled = true
			cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
			cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
			cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
			cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 2
			pool := newOpenAIWSConnPool(cfg)
			defer pool.Close()
			svc := &OpenAIGatewayService{
				cfg: cfg, httpUpstream: &httpUpstreamRecorder{}, cache: &stubGatewayCache{},
				openaiWSResolver: NewOpenAIWSProtocolResolver(cfg), toolCorrector: NewCodexToolCorrector(), openaiWSPool: pool,
			}
			account := &Account{
				ID: 9102, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1,
				Credentials: map[string]any{"api_key": "sk-test", "base_url": server.URL},
				Extra:       map[string]any{"responses_websockets_v2_enabled": true},
			}
			writer := &openAIWSCancelTestSignalWriter{ResponseRecorder: httptest.NewRecorder(), tokenWritten: make(chan struct{})}
			c, _ := gin.CreateTestContext(writer)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
			c.Request.Header.Set("User-Agent", "unit-test-agent/1.0")
			type outcome struct {
				result *OpenAIForwardResult
				err    error
			}
			done := make(chan outcome, 1)
			go func() {
				result, err := svc.Forward(ctx, c, account, []byte(`{"model":"gpt-5.5","stream":true,"reasoning":{"effort":"high"},"tools":[{"type":"image_generation","model":"gpt-image-2","size":"2048x1152"}],"tool_choice":{"type":"image_generation"},"input":[{"type":"input_text","text":"draw a test image"}]}`))
				done <- outcome{result, err}
			}()
			select {
			case <-writer.tokenWritten:
			case <-time.After(3 * time.Second):
				t.Fatal("forwarder did not deliver partial output")
			}
			ap, ok := pool.getAccountPool(account.ID)
			require.True(t, ok)
			var pooled *openAIWSConn
			ap.mu.Lock()
			for _, conn := range ap.conns {
				pooled = conn
			}
			ap.mu.Unlock()
			require.NotNil(t, pooled)
			require.True(t, pooled.hasReaderLoop())
			require.Eventually(t, func() bool {
				if pooled.readMu.TryLock() {
					pooled.readMu.Unlock()
					return false
				}
				return true
			}, time.Second, time.Millisecond, "cancel must happen during a pending pool read")
			cancel()
			// Keep the upstream quiet briefly so the blocked read observes
			// cancellation before usage becomes available on the socket.
			timer := time.AfterFunc(25*time.Millisecond, release)
			defer timer.Stop()
			var got outcome
			select {
			case got = <-done:
			case <-time.After(4 * time.Second):
				t.Fatal("canceled request did not finish draining")
			}
			require.NotNil(t, got.result)
			if terminal {
				require.NoError(t, got.err)
				require.Equal(t, 3, got.result.Usage.InputTokens)
				require.Equal(t, 5, got.result.Usage.OutputTokens)
			} else {
				require.ErrorIs(t, got.err, context.Canceled)
			}
			require.True(t, got.result.ClientDisconnect)
			require.Equal(t, "resp_reader_cancel", got.result.RequestID)
			require.Equal(t, 1, got.result.ImageCount)
			require.Equal(t, []string{"1024x1024"}, got.result.ImageOutputSizes)
			require.Equal(t, "gpt-image-2", got.result.BillingModel)
			require.Equal(t, "2K", got.result.ImageSize)
			require.Equal(t, "2048x1152", got.result.ImageInputSize)
			require.NotNil(t, got.result.RequestedReasoningEffort)
			require.Equal(t, "high", *got.result.RequestedReasoningEffort)
			require.NotContains(t, writer.Body.String(), "response.failed")
		})
	}
}

func TestOpenAIWSConnReaderLoop_DrainCancellationResumesButDeadlineAborts(t *testing.T) {
	ws := newOpenAIWSReaderLoopFakeConn()
	conn := newOpenAIWSConn("drain-cancel", 1, ws, nil)
	defer conn.close()
	require.True(t, conn.tryAcquire())
	lease := &openAIWSConnLease{conn: conn}
	defer lease.Release()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := lease.ReadMessageForDrain(ctx, time.Second)
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, conn.isClosed(), "client cancellation must preserve the socket for usage drain")
	ws.messages <- []byte(`{"type":"response.completed"}`)
	message, err := lease.ReadMessageForDrain(context.WithoutCancel(ctx), time.Second)
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"response.completed"}`, string(message))

	_, err = lease.ReadMessageForDrain(context.Background(), 20*time.Millisecond)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.True(t, conn.isClosed(), "drain must still abort when its read budget expires")
}

type openAIWSCancelTestSignalWriter struct {
	*httptest.ResponseRecorder
	tokenWritten chan struct{}
	once         sync.Once
}

func (w *openAIWSCancelTestSignalWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(p)
	if bytes.Contains(p, []byte(`"delta":"partial"`)) {
		w.once.Do(func() { close(w.tokenWritten) })
	}
	return n, err
}

func runOpenAIWSV2ClientCancellationTest(t *testing.T, captureConn *openAIWSCancelSafeConn, requestBody string) (*OpenAIForwardResult, *cancelOnFirstWriteResponseWriter, error) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	writer := &cancelOnFirstWriteResponseWriter{cancel: cancel}
	c, _ := gin.CreateTestContext(writer)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
	c.Request.Header.Set("User-Agent", "unit-test-agent/1.0")

	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	cfg.Gateway.OpenAIWS.IngressModeDefault = OpenAIWSIngressModeCtxPool
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
	cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8
	cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 5
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3

	pool := newOpenAIWSConnPool(cfg)
	pool.setClientDialerForTest(&openAIWSClientConnCancelDialer{conn: captureConn})
	svc := &OpenAIGatewayService{
		cfg:              cfg,
		httpUpstream:     &httpUpstreamRecorder{},
		cache:            &stubGatewayCache{},
		openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
		toolCorrector:    NewCodexToolCorrector(),
		openaiWSPool:     pool,
	}
	account := &Account{
		ID:          9101,
		Name:        "openai-ws-client-cancel",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test"},
		Extra: map[string]any{
			"openai_apikey_responses_websockets_v2_mode": OpenAIWSIngressModeCtxPool,
		},
	}

	result, err := svc.Forward(ctx, c, account, []byte(requestBody))
	return result, writer, err
}

type cancelOnFirstWriteResponseWriter struct {
	mu     sync.Mutex
	header http.Header
	body   bytes.Buffer
	cancel context.CancelFunc
	writes int
	status int
}

func (w *cancelOnFirstWriteResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *cancelOnFirstWriteResponseWriter) WriteHeader(status int) {
	w.status = status
}

func (w *cancelOnFirstWriteResponseWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.writes == 0 && w.cancel != nil {
		w.cancel()
	}
	w.writes++
	return w.body.Write(p)
}

func (w *cancelOnFirstWriteResponseWriter) Flush() {}

type openAIWSClientConnCancelDialer struct {
	conn openAIWSClientConn
}

func (d *openAIWSClientConnCancelDialer) Dial(
	ctx context.Context,
	wsURL string,
	headers http.Header,
	proxyURL string,
) (openAIWSClientConn, int, http.Header, error) {
	return d.conn, 0, nil, nil
}

// openAIWSCancelSafeConn leaves a delayed event queued when its read context is
// canceled, matching websocket frame semantics and allowing the drain read to
// consume that event with a detached context.
type openAIWSCancelSafeConn struct {
	*openAIWSCaptureConn
}

func (c *openAIWSCancelSafeConn) ReadMessage(ctx context.Context) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errOpenAIWSConnClosed
	}
	if len(c.events) == 0 {
		c.mu.Unlock()
		return nil, io.EOF
	}
	delay := time.Duration(0)
	if len(c.readDelays) > 0 {
		delay = c.readDelays[0]
	}
	event := c.events[0]
	c.mu.Unlock()
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, errOpenAIWSConnClosed
	}
	if len(c.events) == 0 {
		return nil, io.EOF
	}
	c.events = c.events[1:]
	if len(c.readDelays) > 0 {
		c.readDelays = c.readDelays[1:]
	}
	return event, nil
}
