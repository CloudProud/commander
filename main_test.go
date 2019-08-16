package commander

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/jeroenrinzema/commander/types"
)

// TestClosingConsumptions test if consumptions get closed properly.
// This function tests if the message does not get delivered before the sleep period has passed.
func TestClosingConsumptions(t *testing.T) {
	group, client := NewMockClient()
	action := "testing"

	var delivered uint32

	group.HandleFunc(EventMessage, action, func(message *Message, writer Writer) {
		t.Log("received message, going to sleep for 100ms")
		time.Sleep(100 * time.Millisecond)
		t.Log("woke up and mark message as delivered")
		atomic.AddUint32(&delivered, 1)
	})

	event := types.NewMessage(action, 1, nil, nil)
	err := group.ProduceEvent(event)
	if err != nil {
		t.Error(err)
	}

	err = client.Close()
	if err != nil {
		t.Error(err)
	}

	count := atomic.LoadUint32(&delivered)
	if count == 0 {
		t.Error("the client did not close safely unexpected, delivery count:", count)
	}
}
