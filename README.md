# iot-service-com
[Hardwario Tower](https://www.hardwario.com/tower/) IoT communication service to support offline polling of data from baterry powered terminals and broadcasting data to multiple devices. It requires [NATS JetStream](https://docs.nats.io/nats-concepts/jetstream) as persistence layer, which supports both native MQTT protocol used by Hardwario Tower and data persistence required for offline communication.

# Communication topics / subjects
## Device to com service (MQTT)
Some data are sent as raw values (string, int, float). For larger data or more complex data structures, Google protocol buffers is used. Data is then encoded to binary payload using protobuf by source service, and in this configuration service it is split to 32B chunks, each chunk enriched with simple header and sent as base64 encoded strings.

### node/XXX/-/com/cap
Send current device capabilities. Those may change during runtime, for example switching between online and offline modes when solar powered device goes to standby or when device switches to battery backup during power outage.

Example:
```
{com: "off"; dev: "info-box"; acc: ["tim", "met"]}
```
Fields:
* `com`:
  * `off` = offline - information needs to be requested by device (battery powered devices with disabled radio to conserve power),
  * `on` = online - device is always listening, data can be sent anytime,
* `dev` - device name, used in topic name
* `acc` - accepted generic data:
  * `tim` = real time - managed by this configuration service,
  * `met` = current meteorological data - this is managed by separate service and periodically published as binary protobuf payload to all devices able to use it,

### node/XXX/-/com/poll
### node/XXX/-/com/get
### node/XXX/-/com/ack
### node/XXX/-/com/nack

## Com service to device (MQTT)
### node/XXX/com/-/-/fin
### node/XXX/{dev}/conf/-/set
### node/XXX/{dev}/time/-/set
### node/XXX/{dev}/meteo/-/set

## Other services to com service (NATS)

## Com service to other services (NATS)
