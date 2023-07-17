/*
Service providing offline communication and broadcast option for Hardwario Tower devices.
It uses NATS JetStream as both messaging platform and persistent storage

Configuration is provided via environment variables:
- NATS_URL - URL of NATS server
- NATS_CREDS - path to credentials file
- NATS_STREAM_COM - name of JetStream stream for incoming messages
- NATS_STREAM_TOWER - name of JetStream stream for Tower communication
- NATS_SUBJECT_COM - base subject for incoming messages
- NATS_SUBJECT_TOWER - base subject for Hardwario Tower communication
*/
package main

import (
	"encoding/base64"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/ilyakaznacheev/cleanenv"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	"github.com/mixicz/iot-service-com/com-service/cap"
)

type Config struct {
	Nats struct {
		Url          string `env:"NATS_URL" env-default:"nats://localhost:4222"`
		Creds        string `env:"NATS_CREDS" env-default:""`
		StreamCom    string `env:"NATS_STREAM_COM" env-default:"iot_service_com"`
		StreamTower  string `env:"NATS_STREAM_TOWER" env-default:"iot_service_tower"`
		SubjectCom   string `env:"NATS_SUBJECT_COM" env-default:"iot.service.com"`
		SubjectTower string `env:"NATS_SUBJECT_TOWER" env-default:"node"`
	}
}

// Device communication message
type deviceMessage struct {
	Created time.Time
	Ttl     int
	Msg     []string
}

// Device communication state
type deviceState struct {
	Online   bool
	Accept   map[cap.CapData]bool
	Ready    bool
	Timeout  int
	LastSend time.Time
	Queue    map[string]deviceMessage
}

var config Config
var devices map[string]deviceState
var nc *nats.Conn
var js nats.JetStreamContext

// Send time to device
func sendTime(deviceName string) {
	timestamp := time.Now().Unix()
	_, timezoneOffset := time.Now().Zone()
	timeMsg := fmt.Sprintf("%d%+d", timestamp, timezoneOffset)
	log.Printf("Sending time to %s: %s", deviceName, timeMsg)
	err := nc.Publish(config.Nats.SubjectTower+"."+deviceName+".tim.-.set", []byte(timeMsg))
	if err != nil {
		log.Printf("Error sending time to %s: %v", deviceName, err)
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
				log.Printf("Error parsing message: %v", err)
				return
			}
			dev, ok := devices[deviceName]
			if !ok {
				dev = deviceState{
					Online:   capMsg.Onl,
					Accept:   make(map[cap.CapData]bool),
					Ready:    false,
					Timeout:  0,
					LastSend: time.Now(),
					Queue:    make(map[string]deviceMessage),
				}
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
					sendTime(deviceName)
				default:
					// send requested data
					log.Printf("Data (%s) requested by %s", params[0], deviceName)
					conf, err := devices[deviceName].Queue[params[0]]

					// check if data is available and not expired
					if !err && conf.Created.Add(time.Duration(conf.Ttl)*time.Second).After(time.Now()) {
						log.Printf("Queued data available: %s", conf.Msg)
						for _, m := range conf.Msg {
							err := nc.Publish(config.Nats.SubjectTower+"."+deviceName+"."+params[0]+".-.set", []byte(m))
							if err != nil {
								log.Printf("Error sending data: %v", err)
							}
						}
					}
				}
			} else {
				// send all queued messages
				log.Printf("Sending queued messages to %s", deviceName)
				for topic, msg := range devices[deviceName].Queue {
					if msg.Created.Add(time.Duration(msg.Ttl) * time.Second).After(time.Now()) {
						// TTL is ok - send queued message
						log.Printf("Sending queued message: %s -> %s, TTL=%d, queued time=%s", msg.Msg, topic, msg.Ttl, msg.Created.String())
						for _, m := range msg.Msg {
							err := nc.Publish(config.Nats.SubjectTower+"."+deviceName+"."+topic+".-.set", []byte(m))
							if err != nil {
								log.Printf("Error sending queued message: %v", err)
							}
						}
					} else {
						// TTL expired - remove message from queue
						log.Printf("Removing expired message from queue: %s, TTL=%d, queued time=%s", topic, msg.Ttl, msg.Created.String())
						delete(devices[deviceName].Queue, topic)
					}
				}
				// also send time if device accepts it
				if devices[deviceName].Accept[cap.CapData_TIM] {
					sendTime(deviceName)
				}
			}
		case "ack":
			// message successfully received by device
			delete(devices[deviceName].Queue, string(msg.Data))
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
		switch topic {
		case "conf":
			// TODO
		default:
			// TODO
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
