/*
Service providing offline communication and broadcast option for Hardwario Tower devices.
It uses NATS JetStream as both messaging platform and persistent storage

Configuration is provided via environment variables:
- NATS_URL - URL of NATS server
- NATS_CREDS - path to credentials file
- NATS_STREAM_COM - name of JetStream stream for incoming messages
- NATS_STREAM_TOWER - name of JetStream stream for Tower communication
- NATS_STREAM_QUEUE - name of JetStream stream for message queue
- NATS_SUBJECT_COM - base subject for incoming messages
- NATS_SUBJECT_TOWER - base subject for Hardwario Tower communication
- NATS_SUBJECT_QUEUE - base subject for message queue
- NATS_DURABLE_QUEUE - name of durable queue for message queue
*/
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/google/uuid"
	"github.com/ilyakaznacheev/cleanenv"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	"github.com/mixicz/iot-service-com/com-service/cap"
)

const (
	// Maximum Tower message length
	maxMsgLen       = 50
	envelopeDataLen = 32
)

type Config struct {
	Nats struct {
		Url          string `env:"NATS_URL" env-default:"nats://localhost:4222"`
		Creds        string `env:"NATS_CREDS" env-default:""`
		StreamCom    string `env:"NATS_STREAM_COM" env-default:"iot_service_com"`
		StreamTower  string `env:"NATS_STREAM_TOWER" env-default:"iot_service_tower"`
		StreamQueue  string `env:"NATS_STREAM_QUEUE" env-default:"iot_service_com_queue"`
		SubjectCom   string `env:"NATS_SUBJECT_COM" env-default:"iot.service.com"`
		SubjectTower string `env:"NATS_SUBJECT_TOWER" env-default:"node"`
		SubjectQueue string `env:"NATS_SUBJECT_QUEUE" env-default:"iot.service.com._queue"`
		DurableQueue string `env:"NATS_DURABLE_QUEUE" env-default:"iot-com-service"`
		ServiceName  string `env:"NATS_SERVICE_NAME" env-default:"iot-com-service"`
	}
	Event struct {
		TypeQueue       string `env:"EVENT_TYPE_QUEUE" env-default:"iot.service.com.queue"`
		TypeDomainEvent string `env:"EVENT_TYPE_DOMAIN_EVENT" env-default:"iot.service.com.domain_event"`
	}
	DefaultTtl int `env:"DEFAULT_TTL" env-default:"86400"`
}

// tower protobuf envelope
type TowerEnvelope struct {
	Idx  uint8
	Id   uint8
	Data [envelopeDataLen]byte
}

// Device communication message
type deviceMessage struct {
	Created     time.Time
	Ttl         int
	Topic       string
	ContentType string
	Msg         []byte
	NatsMsg     *nats.Msg
}

// Device communication state
type deviceState struct {
	Name         string
	Online       bool
	Accept       map[cap.CapData]bool
	Ready        bool
	Timeout      int
	LastSend     time.Time
	QueueSubject string
	Sub          *nats.Subscription
	SubData      map[string]*nats.Subscription
	// Queue        map[string]deviceMessage
}

var config Config
var devices map[string]deviceState
var nc *nats.Conn
var js nats.JetStreamContext

// Fetches next available message for device from queue
// optional data parameter is used for filtering messages by data type
func (d deviceState) GetNextMessage(data ...string) (deviceMessage, bool) {
	// Check whether we have prepared NATS subscription for requested data
	var sub *nats.Subscription
	if len(data) <= 0 {
		// Any data
		if d.Sub == nil {
			// If subscription is not prepared, subscribe to the queue
			var err error
			d.Sub, err = js.SubscribeSync(d.QueueSubject, nats.Durable(config.Nats.DurableQueue+"-"+d.Name))
			if err != nil {
				log.Printf("Error subscribing to queue: %v", err)
				return deviceMessage{}, false
			}
		}
		sub = d.Sub
	} else {
		// Specific data
		if d.SubData[data[0]] == nil {
			// If subscription is not prepared, subscribe to the queue
			var err error
			d.SubData[data[0]], err = js.SubscribeSync(d.QueueSubject+"."+data[0], nats.Durable(config.Nats.DurableQueue+"-"+d.Name+"-"+data[0]))
			if err != nil {
				log.Printf("Error subscribing to queue: %v", err)
				return deviceMessage{}, false
			}
		}
		sub = d.SubData[data[0]]
	}

	msg, err := sub.NextMsg(0 * time.Second)
	if err == nats.ErrTimeout {
		return deviceMessage{}, false
	} else if err != nil {
		log.Printf("Error getting message from queue: %v", err)
		return deviceMessage{}, false
	}

	// Parse cloud event
	event := cloudevents.NewEvent()
	err = json.Unmarshal(msg.Data, &event)
	if err != nil {
		log.Printf("Error parsing cloud event: %v", err)
		return deviceMessage{}, false
	}

	devMsg := deviceMessage{
		Created:     event.Time(),
		Ttl:         event.Context.GetExtensions()["ttl"].(int),
		Topic:       event.Context.GetExtensions()["topic"].(string),
		ContentType: event.DataContentType(),
		Msg:         event.Data(),
		NatsMsg:     msg,
	}

	return devMsg, true
}

// Ack / Nack message in queue
func (d deviceState) AckMessage(msg deviceMessage, ack bool) {
	if msg.NatsMsg == nil {
		log.Printf("Warning: trying to ACK NATS message without NATS context")
		return
	}
	if ack {
		msg.NatsMsg.Ack()
	} else {
		msg.NatsMsg.Nak()
	}
}

// Add message to queue or send it immediately if device is online
func (d deviceState) AddMessage(msg deviceMessage) {
	// if device is online, send message immediately
	if d.Online {
		d.SendMessage(msg)
		return
	}
	// if device is offline, add message to queue
	// prepare cloud event envelope for queue
	event := cloudevents.NewEvent()
	event.SetID(uuid.NewString())
	event.SetSource(config.Nats.ServiceName)
	event.SetType(config.Event.TypeQueue)
	event.SetTime(msg.Created)
	event.SetData(msg.ContentType, msg.Msg)
	event.Context.SetExtension("ttl", msg.Ttl)
	event.Context.SetExtension("topic", msg.Topic)
	bytes, err := json.Marshal(event)
	if err != nil {
		log.Printf("Error marshalling cloud event to JSON: %v", err)
		return
	}
	// send message to queue
	_, err1 := js.Publish(d.QueueSubject, bytes)
	if err1 != nil {
		log.Printf("Error sending message to queue: %v", err)
	}
}

// Send message to device
func (d deviceState) SendMessage(msg deviceMessage) {
	// split and encode the data
	var parts [][]byte
	switch msg.ContentType {
	case "application/protobuf":
		if len(msg.Msg) > 255*envelopeDataLen {
			log.Printf("ERROR: protobuf message too long (%d > %d), cannot split protobuf messages to more than 255 parts", len(msg.Msg), 255*envelopeDataLen)
			return
		}
		// split and base64 encode parts of protobuf message
		for l := 0; l < len(msg.Msg); l += envelopeDataLen {
			// create envelope
			envelope := TowerEnvelope{
				Idx: uint8(l / envelopeDataLen),
				// use CapData value as ID as it is unique for each topic to allow detection of potential message collisions
				Id:   uint8(cap.CapData_value[strings.ToUpper(msg.Topic)]) & 0x7F,
				Data: [envelopeDataLen]byte{},
			}
			copy(envelope.Data[:], msg.Msg[l:l+envelopeDataLen])
			// last message has MSB of ID set
			if l+envelopeDataLen >= len(msg.Msg) {
				envelope.Id += 0x80
			}
			// encode envelope
			var buf bytes.Buffer
			enc := gob.NewEncoder(&buf)
			err := enc.Encode(envelope)
			if err != nil {
				log.Printf("Error encoding tower envelope to bytes: %v", err)
				return
			}
			encoded := base64.StdEncoding.EncodeToString(buf.Bytes())
			parts = append(parts, []byte(encoded))
		}
	case "text/plain":
		if len(msg.Msg) > maxMsgLen {
			log.Printf("ERROR: text message too long (%d > %d), cannot split plain text messages: %s", len(msg.Msg), maxMsgLen, msg.Msg)
			return
		}
		parts = [][]byte{[]byte(msg.Msg)}
	}

	ok := true
	for _, part := range parts {
		// send messages to device
		err := nc.Publish(config.Nats.SubjectTower+"."+d.Name+"."+msg.Topic+".-.set", part)
		if err != nil {
			ok = false
			log.Printf("Error sending data: %v", err)
		}
	}
	// TODO - ACK after receiving ACK from device, here should be only NACK in case of error
	d.AckMessage(msg, ok)
}

// Initialize device state to sane values
func (d *deviceState) Init() {
	d.SubData = make(map[string]*nats.Subscription)
	d.Sub = nil
	d.Accept = make(map[cap.CapData]bool)
	d.LastSend = time.Now()
}

// Send time to device
func (d deviceState) sendTime() {
	timestamp := time.Now().Unix()
	_, timezoneOffset := time.Now().Zone()
	timeMsg := fmt.Sprintf("%d%+d", timestamp, timezoneOffset)
	log.Printf("Sending time to %s: %s", d.Name, timeMsg)
	err := nc.Publish(config.Nats.SubjectTower+"."+d.Name+".tim.-.set", []byte(timeMsg))
	if err != nil {
		log.Printf("Error sending time to %s: %v", d.Name, err)
	}
}

func sendToDevices(msg deviceMessage, device string) {
	if device == "" {
		for _, dev := range devices {
			if dev.Accept[cap.CapData(cap.CapData_value[strings.ToUpper(msg.Topic)])] {
				dev.AddMessage(msg)
			}
		}
	} else {
		dev, ok := devices[device]
		if ok && dev.Accept[cap.CapData(cap.CapData_value[strings.ToUpper(msg.Topic)])] {
			dev.AddMessage(msg)
		}
	}
}

// NATS handler for incoming messages from TOWER devices
func handleTowerMessage(msg *nats.Msg) {
	log.Printf("Received message from Tower: %s: %s", msg.Subject, string(msg.Data))
	// tokenize subject (example: node.led-pwm:puda:0.fan.0.rpm)
	tokens := strings.Split(msg.Subject, ".")
	// check if message is intended for this service
	if len(tokens) >= 5 && tokens[3] == "com" {
		deviceName := tokens[1]
		command := tokens[4]
		params := tokens[5:]
		switch command {
		case "cap":
			// Update device state with present capabilities
			// parse base64 encoded protobuf message
			data, err := base64.StdEncoding.DecodeString(string(msg.Data))
			if err != nil {
				log.Printf("Error decoding message: %v", err)
				return
			}
			// parse message
			capMsg := &cap.Cap{}
			err = proto.Unmarshal(data, capMsg)
			if err != nil {
				log.Printf("Error unmarshalling protobuf message: %v", err)
				return
			}
			dev, ok := devices[deviceName]
			if !ok {
				dev = deviceState{
					Name:         deviceName,
					Online:       capMsg.Onl,
					Ready:        false,
					Timeout:      0,
					QueueSubject: config.Nats.SubjectQueue + "." + deviceName,
				}
				dev.Init()
				for val := range cap.CapData_name {
					dev.Accept[cap.CapData(val)] = false
				}
				for _, capData := range capMsg.Acc {
					dev.Accept[capData] = true
				}
				log.Printf("New device: %s", deviceName)
			} else {
				dev.Online = capMsg.Onl
				dev.Accept = make(map[cap.CapData]bool)
				for val := range cap.CapData_name {
					dev.Accept[cap.CapData(val)] = false
				}
				dev.Accept[cap.CapData_TIM] = false
				for _, capData := range capMsg.Acc {
					dev.Accept[capData] = true
				}
			}
			devices[deviceName] = dev
		case "get":
			// Device is requesting data
			if len(params) > 0 {
				switch params[0] {
				case "tim":
					// send time
					devices[deviceName].sendTime()
				default:
					// send requested data
					log.Printf("Data (%s) requested by %s", params[0], deviceName)
					// conf, err := devices[deviceName].Queue[params[0]]
					msg, ok := devices[deviceName].GetNextMessage(params[0])

					// check if data is available and not expired
					if ok && msg.Created.Add(time.Duration(msg.Ttl)*time.Second).After(time.Now()) {
						log.Printf("Queued data available: %s", msg.Msg)
						devices[deviceName].SendMessage(msg)
					} else {
						if ok {
							// data expired - remove from the queue by ACKing it
							devices[deviceName].AckMessage(msg, true)
						}
					}
				}
			} else {
				// send all queued messages
				log.Printf("Sending all queued messages to %s", deviceName)
				for msg, ok := devices[deviceName].GetNextMessage(); ok; msg, ok = devices[deviceName].GetNextMessage() {
					if msg.Created.Add(time.Duration(msg.Ttl) * time.Second).After(time.Now()) {
						// TTL is ok - send queued message
						log.Printf("Sending queued message: %s -> %s, TTL=%d, queued time=%s", msg.Msg, msg.Topic, msg.Ttl, msg.Created.String())
						devices[deviceName].SendMessage(msg)
					} else {
						// TTL expired - remove message from queue
						log.Printf("Removing expired message from queue: %s, TTL=%d, queued time=%s", msg.Topic, msg.Ttl, msg.Created.String())
						devices[deviceName].AckMessage(msg, true)
					}
				}
				// also send time if device accepts it
				if devices[deviceName].Accept[cap.CapData_TIM] {
					devices[deviceName].sendTime()
				}
			}
		case "ack":
			// message successfully received by device
			// TODO - remove message from queue (ack? delete?)
		case "nack":
			// TODO - now we don't do anything with this, in future we should resend message up to M times
		}
	}
}

// NATS handler for incoming messages from other services
func handleServiceMessage(msg *nats.Msg) {
	log.Printf("Received message from service: %s: %s", msg.Subject, string(msg.Data))
	// tokenize subject (example: iot.service.com.conf.led-pwm:terasa:0)
	tokens := strings.Split(msg.Subject, ".")
	// check if message is intended for this service
	if len(tokens) >= 4 && tokens[3] == "com" {
		deviceName := ""
		topic := tokens[3]
		if len(tokens) >= 5 {
			deviceName = tokens[4]
		}
		event := cloudevents.NewEvent()
		err := json.Unmarshal(msg.Data, &event)
		if err != nil {
			log.Printf("Error parsing message: %v", err)
			return
		}
		msg := deviceMessage{
			Created:     event.Time(),
			Ttl:         event.Context.GetExtensions()["ttl"].(int),
			Topic:       topic,
			ContentType: event.DataContentType(),
			Msg:         event.Data(),
			NatsMsg:     nil,
		}
		switch event.DataContentType() {
		case "application/protobuf":
			// send / enqueue data
			sendToDevices(msg, deviceName)
		case "text/plain":
			// Check length of message
			if len(event.Data()) > maxMsgLen {
				log.Printf("ERROR: message too long (%d > %d), cannot split plain text messages: %s", len(event.Data()), maxMsgLen, event.Data())
				return
			}
			// send / enqueue data
			sendToDevices(msg, deviceName)
		default:
			log.Printf("ERROR: Unknown data content type: %s", event.DataContentType())
		}
	}
}

func init() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	// read configuration
	err := cleanenv.ReadEnv(&config)
	if err != nil {
		log.Printf("Error reading configuration: %v, using defaults instead", err)
	}

	// initialize devices map
	devices = make(map[string]deviceState)
}

func main() {
	var err error
	// initialize NATS connection
	nc, err = nats.Connect(config.Nats.Url, nats.UserCredentials(config.Nats.Creds))
	if err != nil {
		log.Fatalf("Error connecting to NATS: %v", err)
	}
	defer nc.Close()

	// initialize NATS JetStream
	js, err = nc.JetStream()
	if err != nil {
		log.Fatalf("Error connecting to JetStream: %v", err)
	}

	// subscribe to incoming messages from Tower
	_, err = js.Subscribe(config.Nats.SubjectTower+".>", handleTowerMessage)
	if err != nil {
		log.Fatalf("Error subscribing to Tower messages: %v", err)
	}

	// subscribe to incoming messages from other services
	_, err = js.Subscribe(config.Nats.SubjectCom+".>", handleServiceMessage)
	if err != nil {
		log.Fatalf("Error subscribing to Com messages: %v", err)
	}

	// start HTTP API for health check and metrics
	// TODO

}
