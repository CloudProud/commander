package commander

import (
	"encoding/json"
	"strconv"

	"github.com/Shopify/sarama"
	uuid "github.com/satori/go.uuid"
)

// Event is produced as the result from a command
type Event struct {
	Parent       uuid.UUID       `json:"parent"`
	ID           uuid.UUID       `json:"id"`
	Action       string          `json:"action"`
	Data         json.RawMessage `json:"data"`
	Key          uuid.UUID       `json:"key"`
	Acknowledged bool            `json:"acknowledged"`
	Version      int             `json:"version"`
}

// Populate the event with the data from the given kafka message
func (event *Event) Populate(message *sarama.ConsumerMessage) error {
	for _, header := range message.Headers {
		switch string(header.Key) {
		case ActionHeader:
			event.Action = string(header.Value)
		case ParentHeader:
			parent, err := uuid.FromString(string(header.Value))

			if err != nil {
				return err
			}

			event.Parent = parent
		case IDHeader:
			id, err := uuid.FromString(string(header.Value))

			if err != nil {
				return err
			}

			event.ID = id
		case AcknowledgedHeader:
			acknowledged, err := strconv.ParseBool(string(header.Value))

			if err != nil {
				return err
			}

			event.Acknowledged = acknowledged
		case VersionHeader:
			version, err := strconv.ParseInt(string(header.Value), 10, 0)

			if err != nil {
				return err
			}

			event.Version = int(version)
		}
	}

	id, err := uuid.FromString(string(message.Key))

	if err != nil {
		return err
	}

	event.Key = id
	event.Data = message.Value

	return nil
}
