package commander

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/Shopify/sarama"
	cluster "github.com/bsm/sarama-cluster"
	uuid "github.com/satori/go.uuid"
)

const (
	// ParentHeader kafka message parent header
	ParentHeader = "parent"
	// ActionHeader kafka message action header
	ActionHeader = "action"
	// IDHeader kafka message id header
	IDHeader = "key"
	// AcknowledgedHeader kafka message acknowledged header
	AcknowledgedHeader = "acknowledged"
	// VersionHeader kafka message version header
	VersionHeader = "version"
	// AnyTopic is a const given to the Consume method when wanting to consume "any" topic
	AnyTopic = ""
)

// Commander is a struct that contains all required methods
type Commander struct {
	Consumer      *cluster.Client
	Producer      sarama.SyncProducer
	Timeout       time.Duration
	CommandTopic  string
	EventTopic    string
	ConsumerGroup string

	consumer *Consumer
	closing  chan bool
}

// Consume create and return a new kafka message consumer.
func (commander *Commander) Consume(topics []string) *Consumer {
	consumer := &Consumer{
		Group:  commander.ConsumerGroup,
		Topics: append([]string{commander.CommandTopic, commander.EventTopic}, topics...),
	}
	consumer.Consume(commander.Consumer)

	commander.consumer = consumer
	return consumer
}

// NewEventsConsumer starts consuming the events from the set events topic.
// The returned consumer consumes all events of all actions.
// The topic that gets consumed is set during initialization (commander.EventTopic) of the commander struct.
// All received messages are published over the returned channel.
func (commander *Commander) NewEventsConsumer() (chan *Event, func()) {
	sink := make(chan *Event)
	subscription := commander.consumer.Subscribe(commander.EventTopic)

	go func() {
		for {
			select {
			case <-subscription.closing:
				close(sink)
				return
			case message := <-subscription.messages:
				event := Event{}
				event.Populate(message)
				sink <- &event
			}
		}
	}()

	return sink, func() {
		commander.consumer.UnSubscribe(subscription)
	}
}

// NewEventConsumer starts consuming events of the given action with one of the given version from the set commands topic.
// The topic that gets consumed is set during initialization (commander.EventTopic) of the commander struct.
// All received events are published over the returned go channel.
// The consumer gets closed once a close signal is given to commander.
func (commander *Commander) NewEventConsumer(action string, versions []int) (chan *Event, func()) {
	sink := make(chan *Event)
	subscription := commander.consumer.Subscribe(commander.EventTopic)

	go func() {
		for {
			select {
			case <-subscription.closing:
				close(sink)
				return
			case message := <-subscription.messages:
				var messageAction string
				versionMatch := false

				for _, header := range message.Headers {
					switch string(header.Key) {
					case ActionHeader:
						messageAction = string(header.Value)
					}
				}

				if messageAction != action {
					break
				}

				event := Event{}
				event.Populate(message)

				for _, version := range versions {
					if version == event.Version {
						versionMatch = true
						break
					}
				}

				if !versionMatch {
					break
				}

				sink <- &event
			}
		}
	}()

	return sink, func() {
		commander.consumer.UnSubscribe(subscription)
	}
}

// NewCommandsConsumer starts consuming commands from the set commands topic.
// The topic that gets consumed is set during initialization (commander.CommandTopic) of the commander struct.
// All received messages are send over the returned channel.
func (commander *Commander) NewCommandsConsumer() (chan *Command, func()) {
	sink := make(chan *Command)
	subscription := commander.consumer.Subscribe(commander.CommandTopic)

	go func() {
		for {
			select {
			case <-subscription.closing:
				close(sink)
				return
			case message := <-subscription.messages:
				command := Command{}
				command.Populate(message)
				sink <- &command
			}
		}
	}()

	return sink, func() {
		commander.consumer.UnSubscribe(subscription)
	}
}

// NewCommandConsumer starts consuming commands of the given action from the set commands topic.
// The topic that gets consumed is set during initialization (commander.CommandTopic) of the commander struct.
// All received messages are send over the returned channel.
func (commander *Commander) NewCommandConsumer(action string) (chan *Command, func()) {
	sink := make(chan *Command)
	subscription := commander.consumer.Subscribe(commander.CommandTopic)

	go func() {
		for {
			select {
			case <-subscription.closing:
				close(sink)
				return
			case message := <-subscription.messages:
				match := false
				for _, header := range message.Headers {
					switch string(header.Key) {
					case ActionHeader:
						if string(header.Value) == action {
							match = true
						}
					}
				}

				if !match {
					continue
				}

				command := Command{}
				command.Populate(message)
				sink <- &command
			}
		}
	}()

	return sink, func() {
		commander.consumer.UnSubscribe(subscription)
	}
}

// CommandHandle is a callback function used to handle/process commands
type CommandHandle func(*Command)

// NewCommandHandle is a small wrapper around NewCommandConsumer that awaits till the given action is received.
// Once a command of the given action is received is the CommandHandle callback function called.
// The handle is closed once the consumer receives a close signal.
func (commander *Commander) NewCommandHandle(action string, callback CommandHandle) func() {
	commands, closing := commander.NewCommandConsumer(action)

	go func() {
		for command := range commands {
			callback(command)
		}
	}()

	return closing
}

// EventHandle is a callback function used to handle/process events
type EventHandle func(*Event)

// NewEventHandle is a small wrapper around NewEventConsumer that awaits till the given event is received.
// Once a event of the given action is received is the EventHandle callback called.
// The handle is closed once the consumer receives a close signal.
func (commander *Commander) NewEventHandle(action string, versions []int, callback EventHandle) func() {
	commands, closing := commander.NewEventConsumer(action, versions)

	go func() {
		for command := range commands {
			callback(command)
		}
	}()

	return closing
}

// Produce a new message to kafka.
// A error is returned if anything went wrong in the process.
func (commander *Commander) Produce(message *sarama.ProducerMessage) error {
	_, _, err := commander.Producer.SendMessage(message)

	if err != nil {
		return err
	}

	return nil
}

// AsyncCommand produces a new command but does not wait on the resulting event.
// A async command is usefull for when you are not interested in the result or the command takes too long to wait for.
func (commander *Commander) AsyncCommand(command *Command) error {
	return commander.ProduceCommand(command)
}

// ProduceCommand produces a new command message to the set commands topic
func (commander *Commander) ProduceCommand(command *Command) error {
	message := sarama.ProducerMessage{
		Headers: []sarama.RecordHeader{
			sarama.RecordHeader{
				Key:   []byte("action"),
				Value: []byte(command.Action),
			},
		},
		Key:   sarama.StringEncoder(command.ID.String()),
		Value: sarama.ByteEncoder(command.Data),
		Topic: commander.CommandTopic,
	}

	log.Println("producing command with action:", command.Action)
	return commander.Produce(&message)
}

// SyncCommand produces a new command and waits for the resulting event.
// If the resulting event is not created within the set timeout period will a timeout error be returned.
func (commander *Commander) SyncCommand(command *Command) (*Event, error) {
	err := commander.AsyncCommand(command)

	if err != nil {
		return nil, err
	}

	events, closing := commander.NewEventsConsumer()
	ctx, cancel := context.WithTimeout(context.Background(), commander.Timeout)

	defer closing()
	defer cancel()

	// Wait for event to return
	// A error is thrown if the event does not return within the given period
syncEvent:
	for {
		select {
		case event := <-events:
			if event.Parent != command.ID {
				continue
			}

			return event, nil
		case <-ctx.Done():
			break syncEvent
		}
	}

	return nil, errors.New("request timeout")
}

// ProduceEvent produces a new event message to the set events topic.
func (commander *Commander) ProduceEvent(event *Event) error {
	message := &sarama.ProducerMessage{
		Headers: []sarama.RecordHeader{
			sarama.RecordHeader{
				Key:   []byte(ActionHeader),
				Value: []byte(event.Action),
			},
			sarama.RecordHeader{
				Key:   []byte(ParentHeader),
				Value: []byte(event.Parent.String()),
			},
			sarama.RecordHeader{
				Key:   []byte(IDHeader),
				Value: []byte(event.ID.String()),
			},
			sarama.RecordHeader{
				Key:   []byte(AcknowledgedHeader),
				Value: []byte(strconv.FormatBool(event.Acknowledged)),
			},
			sarama.RecordHeader{
				Key:   []byte(VersionHeader),
				Value: []byte(strconv.Itoa(event.Version)),
			},
		},
		Key:   sarama.StringEncoder(event.Key.String()),
		Topic: commander.EventTopic,
		Value: sarama.ByteEncoder(event.Data),
	}

	log.Println("producing event with action:", event.Action)
	return commander.Produce(message)
}

// BeforeClosing returns a channel that gets published a boolean to before commander gets closed.
func (commander *Commander) BeforeClosing() chan bool {
	if commander.closing == nil {
		commander.closing = make(chan bool)
	}

	return commander.closing
}

// Close the commander consumers, producers and other processes.
func (commander *Commander) Close() {
	if commander.closing != nil {
		close(commander.closing)
	}

	commander.Producer.Close()
	commander.Consumer.Close()
}

// CloseOnSIGTERM closes the commander instance once a SIGTERM signal is send to the process.
func (commander *Commander) CloseOnSIGTERM() {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	<-sigs
	commander.Close()
	os.Exit(0)
}

// NewProducer creates a new kafka produces but panics if something went wrong.
// A kafka config map could be given with additional settings.
func NewProducer(brokers []string, conf *sarama.Config) sarama.SyncProducer {
	conf.Producer.Return.Successes = true
	producer, err := sarama.NewSyncProducer(brokers, conf)

	if err != nil {
		panic(err)
	}

	return producer
}

// NewCommand create a new command with the given action and data.
// A unique ID is generated and set in order to trace the command.
func NewCommand(action string, data []byte) *Command {
	id := uuid.NewV4()

	command := Command{
		ID:     id,
		Action: action,
		Data:   data,
	}

	return &command
}
