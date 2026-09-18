package notes

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func testEvent() Event {
	return Event{
		Type:      "item_toggled",
		NoteID:    uuid.New(),
		ItemID:    uuid.New(),
		Completed: true,
		UserID:    uuid.New(),
	}
}

func TestBrokerPublishSubscribe(t *testing.T) {
	broker := NewBroker()
	ch := broker.Subscribe()
	defer broker.Unsubscribe(ch)

	ev := testEvent()
	broker.Publish(ev)

	select {
	case received := <-ch:
		if received.NoteID != ev.NoteID || received.Completed != ev.Completed {
			t.Fatalf("expected %+v, got %+v", ev, received)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for event")
	}
}

func TestBrokerUnsubscribe(t *testing.T) {
	broker := NewBroker()
	ch := broker.Subscribe()
	broker.Unsubscribe(ch)

	ev := testEvent()
	broker.Publish(ev)

	select {
	case received, ok := <-ch:
		if ok {
			t.Fatalf("unexpected event on unsubscribed channel: %+v", received)
		}
	default:
		// success: nothing pending
	}
}
