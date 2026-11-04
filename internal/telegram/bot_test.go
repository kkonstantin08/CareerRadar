package telegram

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/kkonstantin08/CareerRadar/internal/jobs"
	"github.com/kkonstantin08/CareerRadar/internal/store"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeLinks struct {
	linked int64
	code   string
}

func (f *fakeLinks) LinkTelegram(_ context.Context, code string, id int64) error {
	f.linked = id
	f.code = code
	return nil
}
func (f *fakeLinks) TelegramChat(context.Context) (int64, error) { return f.linked, nil }

func TestLinkOnlyPrivateChatAndOwner(t *testing.T) {
	f := &fakeLinks{}
	b := Bot{links: f}
	if got := b.Handle(context.Background(), Message{ChatID: 100, UserID: 5, ChatType: "group", Text: "/start secret"}); got != "" || f.linked != 0 {
		t.Fatal("linked a group")
	}
	if got := b.Handle(context.Background(), Message{ChatID: 5, UserID: 5, ChatType: "private", Text: "/start secret"}); !strings.Contains(got, "подключён") || f.linked != 5 {
		t.Fatalf("link response %q", got)
	}
	if got := b.Handle(context.Background(), Message{ChatID: 6, UserID: 6, ChatType: "private", Text: "/help"}); got != "" {
		t.Fatalf("responded to another user: %q", got)
	}
}

func TestPollHandlesPrivateLinkMessage(t *testing.T) {
	f := &fakeLinks{}
	b := New(f)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sent := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/botTEST/getUpdates":
			_, _ = io.WriteString(w, `{"ok":true,"result":[{"update_id":1,"message":{"chat":{"id":5,"type":"private"},"from":{"id":5},"text":"/start secret"}}]}`)
		case "/botTEST/sendMessage":
			sent = true
			_, _ = io.WriteString(w, `{"ok":true,"result":{}}`)
			cancel()
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	b.poll(ctx, srv.Client(), srv.URL, "TEST")
	if !sent || f.linked != 5 {
		t.Fatalf("sent=%v linked=%d", sent, f.linked)
	}
}

func TestSendRequiresTelegramConfirmation(t *testing.T) {
	for _, tc := range []struct {
		body string
		want bool
	}{{`{"ok":true}`, true}, {`{"ok":false}`, false}, {`garbled`, false}} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, tc.body) }))
		got := send(context.Background(), srv.Client(), srv.URL, "TEST", 5, "hello")
		srv.Close()
		if got != tc.want {
			t.Fatalf("%s: %t", tc.body, got)
		}
	}
}

type fakeData struct {
	fakeLinks
	status  string
	updated bool
}

func (f *fakeData) Jobs(context.Context) ([]store.Job, error) {
	return []store.Job{{ID: 7, Observation: store.Observation{Title: "Go Intern", URL: "https://example.org/job"}, Status: "Не откликался"}}, nil
}
func (f *fakeData) Sources(context.Context) ([]store.Source, error) {
	return []store.Source{{ID: 7, Name: "Example", URL: "https://example.org", Enabled: true}}, nil
}
func (f *fakeData) SetStatus(_ context.Context, _ int64, status string) error {
	f.status = status
	return nil
}
func (f *fakeData) AddSource(_ context.Context, name, url string, enabled bool) (store.Source, error) {
	return store.Source{Name: name, URL: url, Enabled: enabled}, nil
}
func (f *fakeData) UpdateSource(context.Context, int64, string, string, bool) error {
	f.updated = true
	return nil
}

func TestOwnerCanViewJobsAndChangeStatus(t *testing.T) {
	f := &fakeData{}
	f.linked = 5
	b := New(f)
	if got := b.Handle(context.Background(), Message{ChatID: 5, UserID: 5, ChatType: "private", Text: "/jobs"}); !strings.Contains(got, "Go Intern") {
		t.Fatalf("jobs %q", got)
	}
	got := b.Handle(context.Background(), Message{ChatID: 5, UserID: 5, ChatType: "private", Text: "/status 7 2"})
	if f.status != "Откликнулся" {
		t.Fatalf("status %q: %s", f.status, got)
	}
}

func (f *fakeData) Preferences(context.Context) (jobs.Preferences, error) {
	return jobs.Preferences{}, nil
}

func TestOwnerCanDisableSource(t *testing.T) {
	f := &fakeData{}
	f.linked = 5
	b := New(f)
	got := b.Handle(context.Background(), Message{ChatID: 5, UserID: 5, ChatType: "private", Text: "/disable 7"})
	if !strings.Contains(got, "выключен") || !f.updated {
		t.Fatalf("response %q, updated %t", got, f.updated)
	}
}

type trackedClaims struct {
	fakeLinks
	handled      bool
	claimedAfter bool
}

func (f *trackedClaims) LinkTelegram(ctx context.Context, code string, id int64) error {
	f.handled = true
	return f.fakeLinks.LinkTelegram(ctx, code, id)
}
func (f *trackedClaims) ClaimTelegramUpdate(ctx context.Context, id int64) (bool, error) {
	f.claimedAfter = f.handled
	return true, nil
}
func TestUpdateIsNotAcknowledgedBeforeCommand(t *testing.T) {
	f := &trackedClaims{}
	b := New(f)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/botTEST/getUpdates":
			_, _ = io.WriteString(w, `{"ok":true,"result":[{"update_id":1,"message":{"chat":{"id":5,"type":"private"},"from":{"id":5},"text":"/start secret"}}]}`)
		case "/botTEST/sendMessage":
			_, _ = io.WriteString(w, `{"ok":true}`)
			cancel()
		}
	}))
	defer srv.Close()
	b.poll(ctx, srv.Client(), srv.URL, "TEST")
	if !f.claimedAfter {
		t.Fatal("update claimed before command execution")
	}
}

type atomicStatusLinks struct {
	fakeLinks
	invoked bool
	update  int64
	status  string
}

func (f *atomicStatusLinks) ApplyTelegramStatus(_ context.Context, update, chat, job int64, status string) (bool, error) {
	f.invoked = true
	f.update = update
	f.status = status
	return true, nil
}
func TestStatusUpdateUsesAtomicStore(t *testing.T) {
	f := &atomicStatusLinks{}
	f.linked = 5
	b := New(f)
	handled, reply, err := b.atomicStatus(context.Background(), 42, Message{ChatID: 5, UserID: 5, ChatType: "private", Text: "/status 7 2"})
	if err != nil || !handled || !f.invoked || f.update != 42 || f.status != "Откликнулся" || reply == "" {
		t.Fatalf("atomic %v %q %v %+v", handled, reply, err, f)
	}
}

type atomicSourceLinks struct {
	fakeLinks
	action string
	update int64
}

func (f *atomicSourceLinks) ApplyTelegramSource(_ context.Context, update, chat int64, action string, id int64, name, sourceURL string) (int64, bool, error) {
	f.action = action
	f.update = update
	return 7, true, nil
}
func TestSourceUpdateUsesAtomicStore(t *testing.T) {
	f := &atomicSourceLinks{}
	f.linked = 5
	b := New(f)
	handled, reply, err := b.atomicSource(context.Background(), 42, Message{ChatID: 5, UserID: 5, ChatType: "private", Text: "/add https://example.org/jobs Example"})
	if err != nil || !handled || f.action != "add" || f.update != 42 || !strings.Contains(reply, "7") {
		t.Fatalf("atomic %v %q %v %+v", handled, reply, err, f)
	}
}

type atomicLinkStore struct {
	fakeLinks
	update   int64
	codeUsed string
}

func (f *atomicLinkStore) ApplyTelegramLink(_ context.Context, id int64, code string, chatID int64) (bool, error) {
	f.update = id
	f.codeUsed = code
	return true, nil
}
func TestLinkUpdateUsesAtomicStore(t *testing.T) {
	f := &atomicLinkStore{}
	b := New(f)
	handled, reply, err := b.atomicLink(context.Background(), 55, Message{ChatID: 5, UserID: 5, ChatType: "private", Text: "/start secret"})
	if !handled || err != nil || f.update != 55 || f.codeUsed != "secret" || !strings.Contains(reply, "подключён") {
		t.Fatalf("atomic %v %q %v", handled, reply, err)
	}
}

type atomicCheckLinks struct {
	fakeLinks
	update, source int64
}

func (f *atomicCheckLinks) QueueTelegramCheck(_ context.Context, update, chat, source int64) (bool, error) {
	f.update = update
	f.source = source
	return true, nil
}
func TestCheckUpdateUsesDurableQueue(t *testing.T) {
	f := &atomicCheckLinks{}
	f.linked = 5
	b := New(f)
	handled, reply, err := b.atomicCheck(context.Background(), 55, Message{ChatID: 5, UserID: 5, ChatType: "private", Text: "/check 7"})
	if !handled || err != nil || f.update != 55 || f.source != 7 || !strings.Contains(reply, "очеред") {
		t.Fatalf("atomic %v %q %v", handled, reply, err)
	}
}

type failingReadStore struct{ fakeData }

func (f *failingReadStore) Jobs(context.Context) ([]store.Job, error) {
	return nil, errors.New("database unavailable")
}
func TestReadFailureDoesNotAcknowledgeUpdate(t *testing.T) {
	f := &failingReadStore{}
	f.linked = 5
	b := New(f)
	_, err := b.handle(context.Background(), Message{ChatID: 5, UserID: 5, ChatType: "private", Text: "/jobs"})
	if err == nil {
		t.Fatal("database failure treated as successful command")
	}
}

type conflictingSourceStore struct{ fakeData }

func (f *conflictingSourceStore) ApplyTelegramSource(context.Context, int64, int64, string, int64, string, string) (int64, bool, error) {
	return 0, false, store.ErrSourceURLConflict
}
func (f *conflictingSourceStore) UpdateSource(context.Context, int64, string, string, bool) error {
	return store.ErrSourceURLConflict
}
func TestConflictingSourceEditReturnsTerminalMessage(t *testing.T) {
	f := &conflictingSourceStore{}
	f.linked = 5
	b := New(f)
	msg := Message{ChatID: 5, UserID: 5, ChatType: "private", Text: "/edit 7 https://example.org/used Name"}
	handled, _, err := b.atomicSource(context.Background(), 1, msg)
	if handled || err != nil {
		t.Fatalf("conflict should reach terminal handler: %t %v", handled, err)
	}
	reply, err := b.handle(context.Background(), msg)
	if err != nil || !strings.Contains(reply, "уже") {
		t.Fatalf("reply %q, %v", reply, err)
	}
}

type queuedReplyLinks struct {
	fakeLinks
	cancel   context.CancelFunc
	recorded bool
}

func (f *queuedReplyLinks) RecordTelegramReply(_ context.Context, id, chat int64, reply string) (bool, error) {
	f.recorded = id == 21 && chat == 5 && strings.Contains(reply, "Команды:")
	f.cancel()
	return true, nil
}
func TestReadReplyIsQueuedInsteadOfSentDirectly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := &queuedReplyLinks{cancel: cancel}
	f.linked = 5
	b := New(f)
	sends := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/botTEST/getUpdates":
			_, _ = io.WriteString(w, `{"ok":true,"result":[{"update_id":21,"message":{"chat":{"id":5,"type":"private"},"from":{"id":5},"text":"/help"}}]}`)
		case "/botTEST/sendMessage":
			sends++
			cancel()
			_, _ = io.WriteString(w, `{"ok":true}`)
		}
	}))
	defer srv.Close()
	b.poll(ctx, srv.Client(), srv.URL, "TEST")
	if !f.recorded || sends != 0 {
		t.Fatalf("queued %t direct sends %d", f.recorded, sends)
	}
}

type invalidLinkStore struct {
	fakeLinks
	cancel   context.CancelFunc
	recorded bool
}

func (f *invalidLinkStore) ApplyTelegramLink(context.Context, int64, string, int64) (bool, error) {
	return false, store.ErrInvalidLink
}
func (f *invalidLinkStore) LinkTelegram(context.Context, string, int64) error {
	return store.ErrInvalidLink
}
func (f *invalidLinkStore) RecordInvalidTelegramLink(_ context.Context, id, chat int64) (bool, error) {
	f.recorded = id == 33 && chat == 5
	f.cancel()
	return true, nil
}
func TestInvalidLinkReplyIsDurablyQueued(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := &invalidLinkStore{cancel: cancel}
	b := New(f)
	sends := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/botTEST/sendMessage" {
			sends++
			cancel()
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": []any{map[string]any{"update_id": 33, "message": map[string]any{"chat": map[string]any{"id": 5, "type": "private"}, "from": map[string]any{"id": 5}, "text": "/start invalid"}}}})
	}))
	defer srv.Close()
	b.poll(ctx, srv.Client(), srv.URL, "TEST")
	if !f.recorded || sends != 0 {
		t.Fatalf("queued %t direct sends %d", f.recorded, sends)
	}
}

type failedLookupLinks struct{ fakeLinks }

func (f *failedLookupLinks) TelegramUpdateProcessed(context.Context, int64) (bool, error) {
	return false, errors.New("database unavailable")
}
func TestDatabaseFailureBacksOffBeforePollingAgain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := make(chan struct{}, 3)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls <- struct{}{}
		_, _ = io.WriteString(w, `{"ok":true,"result":[{"update_id":1,"message":{"chat":{"id":5,"type":"private"},"from":{"id":5},"text":"/help"}}]}`)
	}))
	defer srv.Close()
	done := make(chan struct{})
	go func() { New(&failedLookupLinks{}).poll(ctx, srv.Client(), srv.URL, "TEST"); close(done) }()
	<-calls
	select {
	case <-calls:
		cancel()
		<-done
		t.Fatal("polled again without backoff after database failure")
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	<-done
}

type failedReplyQueue struct{ fakeLinks }

func (f *failedReplyQueue) RecordTelegramReply(context.Context, int64, int64, string) (bool, error) {
	return false, errors.New("outbox unavailable")
}

type failedInvalidLinkQueue struct{ fakeLinks }

func (f *failedInvalidLinkQueue) ApplyTelegramLink(context.Context, int64, string, int64) (bool, error) {
	return false, store.ErrInvalidLink
}
func (f *failedInvalidLinkQueue) LinkTelegram(context.Context, string, int64) error {
	return store.ErrInvalidLink
}
func (f *failedInvalidLinkQueue) RecordInvalidTelegramLink(context.Context, int64, int64) (bool, error) {
	return false, errors.New("outbox unavailable")
}
func TestOutboxFailuresBackOffBeforePollingAgain(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		link       links
	}{{"owner reply", "/help", &failedReplyQueue{fakeLinks: fakeLinks{linked: 5}}}, {"invalid link", "/start bad", &failedInvalidLinkQueue{}}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := make(chan struct{}, 3)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls <- struct{}{}
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": []any{map[string]any{"update_id": 1, "message": map[string]any{"chat": map[string]any{"id": 5, "type": "private"}, "from": map[string]any{"id": 5}, "text": tc.text}}}})
			}))
			defer srv.Close()
			done := make(chan struct{})
			go func() { New(tc.link).poll(ctx, srv.Client(), srv.URL, "TEST"); close(done) }()
			<-calls
			select {
			case <-calls:
				cancel()
				<-done
				t.Fatal("polled again without backoff after outbox failure")
			case <-time.After(100 * time.Millisecond):
			}
			cancel()
			<-done
		})
	}
}

type recordedConflictSource struct {
	conflictingSourceStore
	recorded bool
}

func (f *recordedConflictSource) RecordTelegramReply(_ context.Context, update, chat int64, reply string) (bool, error) {
	f.recorded = update == 99 && chat == 5 && strings.Contains(reply, "уже")
	return true, nil
}
func TestSourceConflictIsAcknowledgedWithoutReapplyingMutation(t *testing.T) {
	f := &recordedConflictSource{}
	f.linked = 5
	b := New(f)
	handled, _, err := b.atomicSource(context.Background(), 99, Message{ChatID: 5, UserID: 5, ChatType: "private", Text: "/edit 7 https://example.org/used Name"})
	if err != nil || !handled || !f.recorded {
		t.Fatalf("handled %t recorded %t error %v", handled, f.recorded, err)
	}
}

type missingAtomicStatus struct {
	fakeLinks
	recorded bool
}

func (f *missingAtomicStatus) ApplyTelegramStatus(context.Context, int64, int64, int64, string) (bool, error) {
	return false, sql.ErrNoRows
}
func (f *missingAtomicStatus) RecordTelegramReply(_ context.Context, update, chat int64, reply string) (bool, error) {
	f.recorded = update == 88 && chat == 5 && strings.Contains(reply, "не найдена")
	return true, nil
}
func TestMissingStatusTargetIsAcknowledgedWithoutRetry(t *testing.T) {
	f := &missingAtomicStatus{}
	f.linked = 5
	b := New(f)
	handled, _, err := b.atomicStatus(context.Background(), 88, Message{ChatID: 5, UserID: 5, ChatType: "private", Text: "/status 7 2"})
	if err != nil || !handled || !f.recorded {
		t.Fatalf("handled %t recorded %t error %v", handled, f.recorded, err)
	}
}
