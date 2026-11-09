# BME280 driver

[![GoDoc](https://godoc.org/github.com/bas2k/bme280?status.svg)](https://godoc.org/github.com/bas2k/bme280)
[![CI](https://github.com/bas2k/bme280/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/bas2k/bme280/actions/workflows/ci.yml)
[![Coverage](https://codecov.io/gh/bas2k/bme280/branch/main/graph/badge.svg)](https://codecov.io/gh/bas2k/bme280)

Go driver for the Bosch BME280 sensor.

## Installation

Requires Go 1.26 or newer.

Add the driver to your Go module:

```sh
go get github.com/bas2k/bme280
```

## Device setup

Open an I²C device using your platform's I²C library, then pass it to `bme280.New`.
The device must implement `ReadReg(byte, []byte) error` and
`WriteReg(byte, []byte) error`. Configure the sensor address when opening the
device; `bme280.I2CAddr` is `0x77`.

Call `InitWith` before reading to load the sensor's calibration and configure its
mode and settings. `Init` alone loads calibration but leaves measurement channels
disabled. `Driver.Close` closes the device if it implements `io.Closer`.

See [the API source](bme280.go) for the available modes, settings, and methods.

## Example

This function reads once from an already opened device and closes it before
returning. Pass your platform's device to `readSensor` and handle the returned error.

```go
package example

import (
	"fmt"
	"log"

	"github.com/bas2k/bme280"
)

func readSensor(device interface {
	ReadReg(byte, []byte) error
	WriteReg(byte, []byte) error
}) error {
	driver := bme280.New(device)
	defer func() {
		if err := driver.Close(); err != nil {
			log.Printf("close sensor: %v", err)
		}
	}()

	if err := driver.InitWith(bme280.ModeForced, bme280.Settings{
		Filter:                  bme280.FilterOff,
		PressureOversampling:    bme280.Oversampling16x,
		TemperatureOversampling: bme280.Oversampling16x,
		HumidityOversampling:    bme280.Oversampling16x,
	}); err != nil {
		return fmt.Errorf("initialize sensor: %w", err)
	}

	response, err := driver.Read()
	if err != nil {
		return fmt.Errorf("read sensor: %w", err)
	}
	fmt.Printf("Temperature: %.2f °C, pressure: %.2f hPa, humidity: %.2f %%RH\n",
		response.Temperature, response.Pressure, response.Humidity)
	return nil
}
```

## Measurements

| Response field | Unit |
| --- | --- |
| `Temperature` | Degrees Celsius (°C) |
| `Pressure` | Hectopascals (hPa) |
| `Humidity` | Relative humidity (%RH), from 0 to 100 |

In `ModeForced`, each `Read` triggers a measurement and waits for it to finish.
The sensor returns to sleep afterward; the driver keeps forced mode selected for
the next `Read`. If a conversion is already running, `Read` waits for it before
starting its own measurement. Initialization and forced reads return an error if
the sensor does not become ready within the driver's timeout.

In `ModeNormal`, the sensor measures continuously. `Read` returns an error until
the first conversion can have completed; wait for the maximum conversion time for
the selected oversampling before retrying. `Standby` controls the interval between
normal measurements and does not set the interval between forced reads.
The legacy constant `StandByTime1ms` selects a 0.5 ms standby interval.

`SetSettings` restores the active device mode after applying settings, so normal
measurements resume after the first conversion with the new settings. Enable
temperature, pressure, and humidity oversampling: `Read` returns an error if any
channel is disabled with `OversamplingOff`.

## Compatibility

The exported API is unchanged. Humidity calibration and compensation have been
corrected, so readings may differ from earlier versions. `SetMode` now returns
device communication errors that were previously ignored.

## Development

```sh
./format_check.sh
go test -cover ./...
go build ./...
```

## References

* [Bosch BME280 datasheet](https://www.bosch-sensortec.com/media/boschsensortec/downloads/datasheets/bst-bme280-ds002.pdf)
* [Bosch reference driver](https://github.com/boschsensortec/BME280_SensorAPI)
* [quhar/bme280](https://github.com/quhar/bme280) - another BME280 driver in Go
* [periph.io](https://periph.io/) - a Go peripherals library with BME280 support
