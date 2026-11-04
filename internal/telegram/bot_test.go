package telegram

import (
	"context"
	"github.com/kkonstantin08/CareerRadar/internal/jobs"
	"github.com/kkonstantin08/CareerRadar/internal/store"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
