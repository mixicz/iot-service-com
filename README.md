# iot-service-com
[Hardwario Tower](https://www.hardwario.com/tower/) IoT communication service to support offline polling of data from baterry powered terminals and broadcasting data to multiple devices. It requires [NATS JetStream](https://docs.nats.io/nats-concepts/jetstream) as persistence layer, which supports both native MQTT protocol used by Hardwario Tower and data persistence required for offline communication.

## Responsibilities
* manage device capabilities registry
* offline device communication (data transfer is initiated by the device),
* manage TTL for offline provided data,
* broadcast support (send data to all devices that support them),
* provide current time information for both online and offline devices

## Data format
<mark>TODO</mark>

encapsulation:
* custom JSON?
* custom protobuf?
* (+) cloud event?
  * [protobuf](https://github.com/cloudevents/spec/blob/main/cloudevents/formats/protobuf-format.md)?
  * JSON?

### Device naming convention
All devices should be named in one of formats:
* "{device_category}:{number}" - standard Hardwario Tower naming convention (e.g. "`motion-sensor:3`"),
* "{device_category}:{instance_name}:{number}" - extended naming convention, where *instance_name* can be for example physical location name (e.g. "`motion-sensor:stairwell:1`").

### Binary data encoding
<mark>TODO</mark>
* packet format
* encoding
* communication flow (sequence diagram)

# Communication topics / subjects
## Device to com service (MQTT)
Some data are sent as raw values (string, int, float). For larger data or more complex data structures, Google protocol buffers is used. Data is then encoded to binary payload using protobuf by source service, and in this configuration service it is split to 32B chunks, each chunk enriched with simple header and sent as base64 encoded strings.

### `node/XXX/-/com/cap`
Send current device capabilities. Those may change during runtime, for example switching between online and offline modes when solar powered device goes to standby or when device switches to battery backup during power outage.

**Payload**: <mark>TODO</mark> (JSON vs. protobuf)?
* JSON - simple to debug vs. very limited message size;
* protobuf - ~~have to be base64, pb encode support in firmware~~ pre-generated static string, hard to debug  vs. possible bigger payload in one message.

**Example**:
```
{onl: false; acc: ["tim", "met", "bel"]}
```

**Fields**:
* `onl` (mandatory):
  * `false` = offline - information needs to be requested by the device (battery powered devices with disabled radio to conserve power),
  * `true` = online - device is always listening, data can be sent anytime,
* `acc` (optional) - accepted data as string array, empty array is asumed if not present in payload (used in topic name after device ID):
  * `tim` = real time - managed by this configuration service,
  * `met` = current meteorological data - this is managed by separate service and periodically published as binary protobuf payload to all devices able to use it,
  * `bel` = doorbell events (typically short TTL, for online devices only)
  * `pvc` = PhotoVoltaics Current stats,
  * `pvd` = PhotoVoltaics Daily cumulative stats,

### `node/XXX/-/com/get`, `node/XXX/-/com/get/*`
Request data, either all available data using `/get` or specific data using `/get/<data>`, where `<data>` is the same name as used in `node/XXX/-/com/cap`.

Each received data MUST be acknowledged by `/com/ack` or `/com/nack` message. After last data, `node/XXX/com/-/-/fin` message MUST be sent by the service to indicate no more data will be send and the device can safely turn off radio.

**Payload**: *int* timeout in miliseconds - how long will the device wait for reply before disabling radio. Timeout SHOULD reset after each received data packet to allow receiving large data chunks split to many messages with reasonably short timeout.

### `node/XXX/-/com/ack`, `node/XXX/-/com/nack`
Acknowledge correctly or incorrectly received data. `nack` SHOULD be sent when there is problem parsing the payload (for example missing message for multipart data).

Intended retry behavior:
* `ack` - mark data as successfuly received and remove them from queue,
* `nack` - immediately do up to M retries and repeat for up to next N `get` commands,
* no reply - repeat transfer after next `get` command, up to N times,

**Payload**: *NULL*

## Com service to device (MQTT)
<mark>TODO</mark>

### `node/XXX/com/-/-/fin`
Indicates there are no more data to send and device can safely turn off the radio.

**Payload**: *NULL*

### `node/XXX/{dev}/tim/-/set`
Current timestamp (seconds from 1.1.1970 0:00) and timezone. Communication latencies does not have to be taken into account as this is only simple mechanism to keep device clock aproximately synced with real time and SHOULD NOT be used for applications requiring precise timing. Primary intention is for displaying time to user or cron-like scheduling with minute precission at maximum.

This is handled directly by com service. For offline devices, it is sent as response to specific request for time [`node/XXX/-/com/get/time`](#nodexxx-comget-nodexxx-comget) or as part of generic data request [`node/XXX/-/com/get`](#nodexxx-comget-nodexxx-comget) when `time` is part of capabilities.

**Payload**: *string* "{timestamp}{timezone}", where *timestamp* is standard UNIX timestamp and *timezone* MUST use numerical format "{+/-}HHMM".

**Example**: "1684851276+0200"

### `node/XXX/{dev}/conf/-/set`
Configuration for particular device sent by config service in protobuf format. Com service splits binary payload to small chunks, and sends it to device.

**Payload**: *protobuf* (see [Binary data encoding](#binary-data-encoding))

### `node/XXX/{dev}/met/-/set`
Current meteorological data in protobuf format, periodically broadcasted by meteo service. Com service takes last received meteo data, splits binary payload to small chunks, and sends it to device.

**Payload**: *protobuf* (see [Binary data encoding](#binary-data-encoding) and `interface/meteo.proto`).

### `node/XXX/{dev}/bel/-/trigger`
Notifies of doorbell button push event. How each device responds is in its own responsibility (e.g. LED controllers may blink/flash). Generally is intended to be used with very short TTL, efectively routed to online devices only (very short opportunity window for offline devices polling)

**Payload**: *string* "{ID}", where *ID* is identifier of event source. Can be any string, though "{src_id}@{device_id}" convention is recomended.

**Example**: "a@street:0"

### `node/XXX/{dev}/pvc/-/set`
<mark>TODO</mark>

### `node/XXX/{dev}/pvd/-/set`
<mark>TODO</mark>


## Other services to com service (NATS)
<mark>TODO:</mark>

* subject/topic naming conventions,
  * `iot.service.com.<data type>[.<target device>]`
    * `iot.service.com.meteo`
    * `iot.service.com.conf.led-pwm:terasa:0`
* topics:
  * met
  * pvc
  * pvd
  * conf
* broadcast messages,
  * TTL of message,
* generic messages (e.g. meteo),
* raw payload vs. protobuf,
* configuration provisioning,

## Com service to other services (NATS)
<mark>TODO</mark>

* domain events?