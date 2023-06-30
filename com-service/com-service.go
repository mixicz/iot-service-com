/*
Service providing offline communication and broadcast option for Hardwario Tower devices.
It uses NATS JetStream as both messaging platform and persistent storage

Configuration is provided via environment variables:
- NATS_URL - URL of NATS server
- NATS_CREDS - path to credentials file
- NATS_STREAM_COM - name of JetStream stream for incoming messages
- NATS_SUBJECT_COM - base subject for incoming messages
- NATS_SUBJECT_TOWER - base subject for Hardwario Tower communication
*/
package main

import (
	"log"
	"time"

	"github.com/ilyakaznacheev/cleanenv"
	"github.com/nats-io/nats.go"
)

type Config struct {
	Nats struct {
		Url          string `env:"NATS_URL" env-default:"nats://localhost:4222"`
		Creds        string `env:"NATS_CREDS" env-default:""`
		StreamCom    string `env:"NATS_STREAM_COM" env-default:"iot_service_com`
		SubjectCom   string `env:"NATS_SUBJECT_COM" env-default:"iot.service.com"`
		SubjectTower string `env:"NATS_SUBJECT_TOWER" env-default:"node"`
	}
}

// Device communication message
type deviceMessage struct {
	Created time.Time
	Ttl     int
	Msg     string
}

// Device communication state
type deviceState struct {
	Online   bool
	Ready    bool
	Timeout  int
	LastSend time.Time
	Queue    []deviceMessage
}

var config Config

func handleTowerMessage(msg *nats.Msg) {
	log.Printf("Received message from Tower: %s", string(msg.Data))
}

func init() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
}

func main() {
	err := cleanenv.ReadEnv(&config)
	if err != nil {
		log.Printf("Error reading configuration: %v, using defaults instead", err)
	}

	// initialize NATS connection
	nc, err := nats.Connect(config.Nats.Url, nats.UserCredentials(config.Nats.Creds))
	if err != nil {
		log.Fatalf("Error connecting to NATS: %v", err)
	}
	defer nc.Close()

	// initialize NATS JetStream
	js, err := nc.JetStream()
	if err != nil {
		log.Fatalf("Error connecting to JetStream: %v", err)
	}

	// subscribe to incoming messages from Tower
	_, err = nc.Subscribe(config.Nats.SubjectTower+".>", handleTowerMessage)
	if err != nil {
		log.Fatalf("Error subscribing to Tower messages: %v", err)
	}
}
