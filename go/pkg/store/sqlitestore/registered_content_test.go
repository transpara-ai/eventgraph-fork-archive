package sqlitestore

import (
	"encoding/json"
	"testing"

	"github.com/transpara-ai/eventgraph/go/pkg/event"
)

type registeredContent struct {
	Outcome string `json:"outcome"`
}

func (registeredContent) EventTypeName() string            { return "test.sqlite.registered" }
func (registeredContent) Accept(event.EventContentVisitor) {}

func TestRegisteredContentDecoder(t *testing.T) {
	event.RegisterContentUnmarshaler("test.sqlite.registered", event.Unmarshal[registeredContent])
	content, err := unmarshalContent("test.sqlite.registered", []byte(`{"outcome":"persisted"}`))
	if err != nil {
		t.Fatal(err)
	}
	if actual, ok := content.(registeredContent); !ok || actual.Outcome != "persisted" {
		t.Fatalf("registered content = %#v", content)
	}
	if _, err := unmarshalContent("test.sqlite.registered", json.RawMessage(`{broken`)); err == nil {
		t.Fatal("invalid custom content accepted")
	}
}

func TestHeadDistinguishesEmptyFromUnavailableOrCorrupt(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	head, err := s.Head()
	if err != nil || head.IsSome() {
		t.Fatalf("empty head=%v %v", head, err)
	}
	_, err = s.db.Exec(`INSERT INTO events(event_id,event_type,version,timestamp_nanos,source,content,causes,conversation_id,hash,prev_hash,signature) VALUES ('invalid','unknown.unregistered',1,1,'actor_invalid','{}','[]','conv_invalid','invalid','invalid',x'00')`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Head(); err == nil {
		t.Fatal("unreadable head was treated as empty")
	}
	s.Close()
	if _, err = s.Head(); err == nil {
		t.Fatal("closed store was treated as empty")
	}
}
