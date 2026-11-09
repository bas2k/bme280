package bme280

import (
	"encoding/binary"
	"errors"
	"math"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

type nullBus struct{}

func (*nullBus) ReadReg(byte, []byte) error {
	return nil
}

func (*nullBus) WriteReg(byte, []byte) error {
	return nil
}

func TestRead(t *testing.T) {
	t.Run("fails if uninitialized", func(t *testing.T) {
		driver := New(&nullBus{})

		_, err := driver.Read()
		if err == nil || !strings.HasPrefix(err.Error(), "driver uninitialized") {
			t.Errorf("no or unexpected error: %v", err)
		}
	})
}

type closerBus struct {
	closed bool
}

func (b *closerBus) ReadReg(byte, []byte) error {
	return nil
}

func (b *closerBus) WriteReg(byte, []byte) error {
	return nil
}

func (b *closerBus) Close() error {
	b.closed = true
	return nil
}

func TestClose(t *testing.T) {
	b := closerBus{}
	err := New(&b).Close()
	if err != nil {
		t.Fatalf("close: %v", err)
	}

	if !b.closed {
		t.Errorf("expected closed")
	}
}

type registerBus struct {
	registers           [256]byte
	reads, writes       int
	failRead, failWrite int
	err                 error
}

func (b *registerBus) ReadReg(reg byte, data []byte) error {
	b.reads++
	if b.reads == b.failRead {
		return b.err
	}
	copy(data, b.registers[int(reg):])
	return nil
}

func (b *registerBus) WriteReg(reg byte, data []byte) error {
	b.writes++
	if b.writes == b.failWrite {
		return b.err
	}
	if reg == resetAddr && data[0] == 0xB6 {
		b.registers[ctrlHumAddr], b.registers[ctrlMeasAddr], b.registers[configAddr] = 0, 0, 0
		copy(b.registers[dataAddr:], []byte{0x80, 0, 0, 0x80, 0, 0, 0x80, 0})
	}
	copy(b.registers[int(reg):], data)
	return nil
}

func calibratedBus() *registerBus {
	b := &registerBus{}
	b.registers[chipIdAddr] = chipId
	// Bosch temperature/pressure example calibration, plus the humidity fixture below.
	for i, v := range []int32{27504, 26435, -1000, 36477, -10685, 3024, 2855, 140, -7, 15500, -14600, 6000} {
		binary.LittleEndian.PutUint16(b.registers[int(tempPressCalibDataAddr)+2*i:], uint16(v))
	}
	b.registers[0xA1] = 75
	binary.LittleEndian.PutUint16(b.registers[humidityCalibDataAddr:], 362)
	b.registers[0xE4], b.registers[0xE5], b.registers[0xE6], b.registers[0xE7] = 0x14, 0x25, 0x03, 30
	return b
}

func TestReadMeasurement(t *testing.T) {
	for _, mode := range []Mode{ModeNormal, ModeForced} {
		t.Run(mode.String(), func(t *testing.T) {
			b := calibratedBus()
			d := New(b)
			if err := d.InitWith(mode, Settings{
				TemperatureOversampling: Oversampling1x,
				PressureOversampling:    Oversampling1x,
				HumidityOversampling:    Oversampling1x,
			}); err != nil {
				t.Fatal(err)
			}
			// Completed conversion: pressure 415148, temperature 519888, humidity 30000.
			copy(b.registers[dataAddr:], []byte{0x65, 0x5A, 0xC0, 0x7E, 0xED, 0, 0x75, 0x30})
			if mode == ModeForced {
				b.registers[ctrlMeasAddr] &^= 3
			} else {
				time.Sleep(10 * time.Millisecond)
			}
			for range 2 {
				writes := b.writes
				got, err := d.Read()
				if err != nil {
					t.Fatal(err)
				}
				if math.Abs(got.Temperature-25.08) > 0.01 || math.Abs(got.Pressure-1006.53) > 0.02 || math.Abs(got.Humidity-50.72) > 0.15 {
					t.Fatalf("unexpected measurement: %+v", got)
				}
				if mode == ModeForced && (b.writes-writes != 2 || Mode(b.registers[ctrlMeasAddr]&3) != ModeForced) {
					t.Fatal("Read did not trigger a forced conversion")
				}
				// Model the automatic return to sleep after a forced conversion.
				if mode == ModeForced {
					b.registers[ctrlMeasAddr] &^= 3
				}
			}
		})
	}
}

func TestReadMissingMeasurement(t *testing.T) {
	for _, tt := range []struct {
		name     string
		settings Settings
	}{
		{"not ready", Settings{TemperatureOversampling: Oversampling1x, PressureOversampling: Oversampling1x, HumidityOversampling: Oversampling1x}},
		{"temperature disabled", Settings{PressureOversampling: Oversampling1x, HumidityOversampling: Oversampling1x}},
		{"pressure disabled", Settings{TemperatureOversampling: Oversampling1x, HumidityOversampling: Oversampling1x}},
		{"humidity disabled", Settings{TemperatureOversampling: Oversampling1x, PressureOversampling: Oversampling1x}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b := calibratedBus()
			d := New(b)
			if err := d.InitWith(ModeSleep, tt.settings); err != nil {
				t.Fatal(err)
			}
			// Ordinary data must not make a disabled or unstarted channel readable.
			copy(b.registers[dataAddr:], []byte{0x65, 0x5A, 0xC0, 0x7E, 0xED, 0, 0x75, 0x30})
			wantError := "disabled"
			if tt.name == "not ready" {
				wantError = "not ready"
			}
			if got, err := d.Read(); err == nil || got != (Response{}) || !strings.Contains(err.Error(), wantError) {
				t.Fatalf("Read = %+v, %v; want empty response and error", got, err)
			}
		})
	}
}

func TestReadMidscaleMeasurement(t *testing.T) {
	for _, mode := range []Mode{ModeNormal, ModeForced} {
		for _, tt := range []struct {
			name string
			data [8]byte
		}{
			{"temperature", [8]byte{0x65, 0x5A, 0xC0, 0x80, 0, 0, 0x75, 0x30}},
			{"pressure", [8]byte{0x80, 0, 0, 0x7E, 0xED, 0, 0x75, 0x30}},
			{"humidity", [8]byte{0x65, 0x5A, 0xC0, 0x7E, 0xED, 0, 0x80, 0}},
			{"all channels", [8]byte{0x80, 0, 0, 0x80, 0, 0, 0x80, 0}},
		} {
			t.Run(mode.String()+"/"+tt.name, func(t *testing.T) {
				b := calibratedBus()
				d := New(b)
				if err := d.InitWith(mode, Settings{TemperatureOversampling: Oversampling1x, PressureOversampling: Oversampling1x, HumidityOversampling: Oversampling1x}); err != nil {
					t.Fatal(err)
				}
				copy(b.registers[dataAddr:], tt.data[:])
				if mode == ModeForced {
					b.registers[ctrlMeasAddr] &^= 3
				} else {
					time.Sleep(10 * time.Millisecond)
				}
				if _, err := d.Read(); err != nil {
					t.Fatalf("completed midscale measurement rejected: %v", err)
				}
			})
		}
	}
}

func TestReadNormalReadiness(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := calibratedBus()
		d := New(b)
		if err := d.InitWith(ModeNormal, Settings{TemperatureOversampling: Oversampling16x, PressureOversampling: Oversampling16x, HumidityOversampling: Oversampling16x}); err != nil {
			t.Fatal(err)
		}
		if got, err := d.Read(); err == nil || got != (Response{}) {
			t.Fatalf("first conversion not ready: Read = %+v, %v", got, err)
		}
		time.Sleep(112800 * time.Microsecond)
		if _, err := d.Read(); err != nil {
			t.Fatalf("completed conversion: %v", err)
		}
		if err := d.Sleep(); err != nil {
			t.Fatal(err)
		}
		copy(b.registers[dataAddr:], []byte{0x65, 0x5A, 0xC0, 0x7E, 0xED, 0, 0x75, 0x30})
		if got, err := d.Read(); err == nil || got != (Response{}) {
			t.Fatalf("reset invalidated conversion: Read = %+v, %v", got, err)
		}
	})
}

func TestReadAfterSettingsChange(t *testing.T) {
	b := calibratedBus()
	d := New(b)
	s := Settings{TemperatureOversampling: Oversampling1x, PressureOversampling: Oversampling1x, HumidityOversampling: Oversampling1x}
	if err := d.InitWith(ModeForced, s); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	b.registers[ctrlMeasAddr] &^= 3
	if err := d.SetMode(ModeSleep); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Read(); err != nil {
		t.Fatalf("completed sample in sleep mode: %v", err)
	}
	if err := d.SetSettings(s); err != nil {
		t.Fatal(err)
	}
	if got, err := d.Read(); err == nil || got != (Response{}) {
		t.Fatalf("settings invalidated conversion: Read = %+v, %v", got, err)
	}
}

func TestReadSettingsErrors(t *testing.T) {
	for step := 1; step <= 3; step++ {
		b := calibratedBus()
		d := New(b)
		if err := d.InitWith(ModeSleep, Settings{TemperatureOversampling: Oversampling1x, PressureOversampling: Oversampling1x, HumidityOversampling: Oversampling1x}); err != nil {
			t.Fatal(err)
		}
		b.failRead, b.err = b.reads+step, errors.New("settings read failed")
		writes := b.writes
		if got, err := d.Read(); err != b.err || got != (Response{}) || b.writes != writes {
			t.Fatalf("step=%d: Read = %+v, %v; writes=%d, want %d", step, got, err, b.writes, writes)
		}
	}
}

type timedConversionBus struct {
	*registerBus
	conversionTime time.Duration
	readyAt        time.Time
	earlyRead      bool
	statusErr      error
}

func (b *timedConversionBus) WriteReg(reg byte, data []byte) error {
	if err := b.registerBus.WriteReg(reg, data); err != nil {
		return err
	}
	if reg == ctrlMeasAddr && data[0]&3 == byte(ModeForced) {
		b.readyAt = time.Now().Add(b.conversionTime)
		b.registers[statusAddr] |= 0x08
	}
	return nil
}

func (b *timedConversionBus) ReadReg(reg byte, data []byte) error {
	if !b.readyAt.IsZero() {
		if time.Now().Before(b.readyAt) {
			if reg == dataAddr {
				b.earlyRead = true
			}
		} else {
			copy(b.registers[dataAddr:], []byte{0x65, 0x5A, 0xC0, 0x7E, 0xED, 0, 0x75, 0x30})
			b.registers[ctrlMeasAddr] &^= 3
			b.registers[statusAddr] &^= 0x08
			b.readyAt = time.Time{}
		}
	}
	if reg == statusAddr && b.statusErr != nil {
		return b.statusErr
	}
	return b.registerBus.ReadReg(reg, data)
}

func TestReadWaitsForNewConversion(t *testing.T) {
	for _, tt := range []struct {
		oversampling Oversampling
		duration     time.Duration
	}{
		{Oversampling1x, 9300 * time.Microsecond},
		{Oversampling16x, 112800 * time.Microsecond},
	} {
		t.Run(tt.oversampling.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				b := &timedConversionBus{registerBus: calibratedBus(), conversionTime: tt.duration}
				d := New(b)
				if err := d.InitWith(ModeForced, Settings{TemperatureOversampling: tt.oversampling, PressureOversampling: tt.oversampling, HumidityOversampling: tt.oversampling}); err != nil {
					t.Fatal(err)
				}
				for range 2 {
					got, err := d.Read()
					if err != nil || b.earlyRead || !b.readyAt.IsZero() || math.Abs(got.Temperature-25.08) > 0.01 {
						t.Fatalf("Read = %+v, %v; early=%v, conversion pending=%v", got, err, b.earlyRead, !b.readyAt.IsZero())
					}
				}
			})
		})
	}
}

func TestReadNewConversionCompletion(t *testing.T) {
	busErr := errors.New("status read failed")
	for _, tt := range []struct {
		name      string
		extraTime time.Duration
		statusErr error
		wantError bool
	}{
		{"delayed completion", 4 * time.Millisecond, nil, false},
		{"stuck conversion", time.Second, nil, true},
		{"status read fails", 0, busErr, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := Settings{TemperatureOversampling: Oversampling1x, PressureOversampling: Oversampling1x, HumidityOversampling: Oversampling1x}
				b := &timedConversionBus{registerBus: calibratedBus(), conversionTime: s.measurementTime()}
				d := New(b)
				if err := d.InitWith(ModeForced, s); err != nil {
					t.Fatal(err)
				}
				if _, err := d.Read(); err != nil {
					t.Fatal(err)
				}
				b.conversionTime += tt.extraTime
				b.statusErr = tt.statusErr
				start := time.Now()
				got, err := d.Read()
				if (err != nil) != tt.wantError || b.earlyRead {
					t.Fatalf("Read = %+v, %v; early=%v, want error=%v", got, err, b.earlyRead, tt.wantError)
				}
				if tt.wantError && got != (Response{}) {
					t.Fatalf("failed conversion returned a sample: %+v", got)
				}
				if tt.statusErr != nil && err != tt.statusErr {
					t.Fatalf("error = %v, want %v", err, tt.statusErr)
				}
				if tt.name == "stuck conversion" && (!strings.Contains(err.Error(), "timed out") || time.Since(start) > s.measurementTime()+10*time.Millisecond) {
					t.Fatalf("error = %v after %v; want bounded conversion timeout", err, time.Since(start))
				}
				if !tt.wantError && (!b.readyAt.IsZero() || math.Abs(got.Temperature-25.08) > 0.01) {
					t.Fatalf("completed conversion: response=%+v, pending=%v", got, !b.readyAt.IsZero())
				}
			})
		})
	}
}

func TestSetSettings(t *testing.T) {
	for _, mode := range []Mode{ModeSleep, ModeNormal, ModeForced} {
		t.Run(mode.String(), func(t *testing.T) {
			b := calibratedBus()
			d := New(b)
			if err := d.InitWith(mode, Settings{}); err != nil {
				t.Fatal(err)
			}
			want := Settings{Filter4, StandByTime500ms, Oversampling2x, Oversampling4x, Oversampling8x}
			if err := d.SetSettings(want); err != nil {
				t.Fatal(err)
			}
			if got, err := d.GetSettings(); err != nil || got != want {
				t.Fatalf("settings = %+v, %v; want %+v", got, err, want)
			}
			if got, err := d.GetMode(); err != nil || got != mode || d.mode != mode {
				t.Fatalf("hardware mode = %v, selected mode = %v, error = %v; want %v", got, d.mode, err, mode)
			}
		})
	}
}

func TestSetSettingsValues(t *testing.T) {
	for _, tt := range []struct {
		name     string
		settings Settings
		invalid  bool
	}{
		{"filter too large", Settings{Filter: 8}, true},
		{"standby too large", Settings{Standby: 8}, true},
		{"pressure too large", Settings{PressureOversampling: 8}, true},
		{"temperature too large", Settings{TemperatureOversampling: 8}, true},
		{"humidity too large", Settings{HumidityOversampling: 8}, true},
		{"maximum byte", Settings{255, 255, 255, 255, 255}, true},
		{"zero", Settings{}, false},
		{"aliases six", Settings{6, 6, 6, 6, 6}, false},
		{"aliases seven", Settings{7, 7, 7, 7, 7}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b := calibratedBus()
			b.registers[ctrlMeasAddr] = byte(ModeNormal)
			d := New(b)
			err := d.SetSettings(tt.settings)
			if tt.invalid {
				if err == nil || b.reads != 0 || b.writes != 0 {
					t.Fatalf("invalid settings: error=%v, reads=%d, writes=%d", err, b.reads, b.writes)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got, err := d.GetSettings(); err != nil || got != tt.settings {
				t.Fatalf("settings = %+v, %v; want %+v", got, err, tt.settings)
			}
		})
	}
}

func TestInitWithInvalidSettings(t *testing.T) {
	d := New(calibratedBus())
	err := d.InitWith(ModeNormal, Settings{PressureOversampling: 8})
	if err == nil || !strings.Contains(err.Error(), "invalid sensor settings") {
		t.Fatalf("InitWith error = %v; want invalid settings error", err)
	}
}

func TestSetSettingsErrors(t *testing.T) {
	want := Settings{Filter4, StandByTime500ms, Oversampling2x, Oversampling4x, Oversampling8x}
	b := &registerBus{}
	b.registers[ctrlMeasAddr] = byte(ModeNormal)
	if err := New(b).SetSettings(want); err != nil {
		t.Fatal(err)
	}
	busErr := errors.New("bus failure")
	for _, reading := range []bool{true, false} {
		count := b.writes
		if reading {
			count = b.reads
		}
		for step := 1; step <= count; step++ {
			broken := &registerBus{err: busErr}
			broken.registers[ctrlMeasAddr] = byte(ModeNormal)
			if reading {
				broken.failRead = step
			} else {
				broken.failWrite = step
			}
			if err := New(broken).SetSettings(want); err != busErr {
				t.Fatalf("reading=%v, step=%d: error = %v, want %v", reading, step, err, busErr)
			}
		}
	}
}

func TestTemperatureLimits(t *testing.T) {
	d := New(calibratedBus())
	if err := d.readCalibData(); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		raw  uint32
		want float64
	}{
		{0, -40},
		{519888, 25.08},
		{0xFFFFF, 85},
	} {
		if got, _ := d.compensateTemperature(tt.raw); got != tt.want {
			t.Errorf("raw=%d: temperature = %v °C, want %v °C", tt.raw, got, tt.want)
		}
	}
}

func TestPressureFallback(t *testing.T) {
	d := New(&nullBus{})
	if got := d.compensatePressure(415148); got != 300 {
		t.Fatalf("pressure = %v hPa, want 300 hPa", got)
	}
}

func TestPressureScalingOverflow(t *testing.T) {
	d := New(calibratedBus())
	if err := d.readCalibData(); err != nil {
		t.Fatal(err)
	}
	d.calib.p1 = 10000
	_, d.calib.tFine = d.compensateTemperature(519888)
	// Bosch's 64-bit reference clamps this synthetic calibration to 1100 hPa.
	if got := d.compensatePressure(415148); got != 1100 {
		t.Fatalf("pressure = %v hPa, want 1100 hPa", got)
	}
}

func TestSetMode(t *testing.T) {
	for _, tt := range []struct {
		name                string
		mode                Mode
		failRead, failWrite int
	}{
		{"get mode error", ModeSleep, 1, 0},
		{"sleep read error", ModeNormal, 2, 0},
		{"sleep reset error", ModeNormal, 0, 1},
		{"mode read error", ModeSleep, 2, 0},
		{"mode write error", ModeSleep, 0, 1},
		{"success", ModeSleep, 0, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b := &registerBus{failRead: tt.failRead, failWrite: tt.failWrite, err: errors.New("bus failure")}
			b.registers[pwrCtrlAddr] = 0xB4 | byte(tt.mode)
			d := New(b)
			d.mode = tt.mode
			err := d.SetMode(ModeForced)
			if tt.failRead != 0 || tt.failWrite != 0 {
				if err != b.err {
					t.Fatalf("SetMode error = %v, want %v", err, b.err)
				}
				if d.mode != tt.mode {
					t.Fatalf("mode changed after failure: %v", d.mode)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if d.mode != ModeForced || b.registers[pwrCtrlAddr] != 0xB5 {
				t.Fatalf("mode = %v, register = %02X", d.mode, b.registers[pwrCtrlAddr])
			}
		})
	}
}

func TestSetModeValues(t *testing.T) {
	for _, mode := range []Mode{ModeSleep, ModeForced, 2, ModeNormal, 4, 5, 255} {
		t.Run(mode.String(), func(t *testing.T) {
			b := &registerBus{}
			d := New(b)
			err := d.SetMode(mode)
			if mode > ModeNormal {
				if err == nil || b.reads != 0 || b.writes != 0 || d.mode != ModeSleep {
					t.Fatalf("invalid mode touched device or succeeded: error=%v, reads=%d, writes=%d, mode=%v", err, b.reads, b.writes, d.mode)
				}
				return
			}
			want := mode
			if mode == 2 {
				want = ModeForced
			}
			if err != nil || d.mode != want || Mode(b.registers[ctrlMeasAddr]&3) != want {
				t.Fatalf("mode=%v, register=%02X, error=%v; want %v", d.mode, b.registers[ctrlMeasAddr], err, want)
			}
		})
	}
}

type startupBus struct {
	*registerBus
	readyAt    time.Time
	startupErr error
	chipReads  int
}

func (b *startupBus) ReadReg(reg byte, data []byte) error {
	if reg == chipIdAddr {
		b.chipReads++
		if time.Now().Before(b.readyAt) {
			data[0] = 0
			return b.startupErr
		}
	}
	return b.registerBus.ReadReg(reg, data)
}

func TestInitWaitsForStartup(t *testing.T) {
	busErr := errors.New("sensor starting")
	for _, tt := range []struct {
		name       string
		delay      time.Duration
		startupErr error
		wantError  bool
	}{
		{"delayed chip ID", 2 * time.Millisecond, nil, false},
		{"delayed communication", 2 * time.Millisecond, busErr, false},
		{"missing chip ID", time.Second, nil, true},
		{"failed communication", time.Second, busErr, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				b := &startupBus{registerBus: calibratedBus(), readyAt: time.Now().Add(tt.delay), startupErr: tt.startupErr}
				d := New(b)
				start := time.Now()
				err := d.Init()
				if (err != nil) != tt.wantError || d.initialized == tt.wantError {
					t.Fatalf("Init error=%v, initialized=%v; want error=%v", err, d.initialized, tt.wantError)
				}
				if tt.wantError {
					if b.chipReads != 6 || time.Since(start) != 10*time.Millisecond {
						t.Fatalf("chip reads=%d, elapsed=%v; want 6 reads over 10ms", b.chipReads, time.Since(start))
					}
					if tt.startupErr != nil && err != tt.startupErr {
						t.Fatalf("error=%v, want %v", err, tt.startupErr)
					}
				}
			})
		})
	}
}

type resetBus struct {
	*registerBus
	busyReads int
}

func (b *resetBus) ReadReg(reg byte, data []byte) error {
	if err := b.registerBus.ReadReg(reg, data); err != nil {
		return err
	}
	if reg == statusAddr && b.busyReads != 0 {
		data[0] |= 1
		if b.busyReads > 0 {
			b.busyReads--
		}
	}
	return nil
}

func TestInitWaitsForCalibration(t *testing.T) {
	for _, tt := range []struct {
		name      string
		busyReads int
		failRead  int
		wantError bool
	}{
		{"ready", 0, 0, false},
		{"delayed", 2, 0, false},
		{"stuck", -1, 0, true},
		{"status read fails", 0, 2, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b := &resetBus{registerBus: calibratedBus(), busyReads: tt.busyReads}
			b.failRead, b.err = tt.failRead, errors.New("status read failed")
			d := New(b)
			err := d.Init()
			if (err != nil) != tt.wantError || d.initialized == tt.wantError {
				t.Fatalf("Init error=%v, initialized=%v; want error=%v", err, d.initialized, tt.wantError)
			}
			if tt.failRead != 0 && err != b.err {
				t.Fatalf("error=%v, want %v", err, b.err)
			}
			if tt.busyReads > 0 && b.busyReads != 0 {
				t.Fatal("Init returned before calibration was ready")
			}
		})
	}
}

type conversionBus struct {
	*registerBus
	modeReads  int
	overlapped bool
}

func (b *conversionBus) ReadReg(reg byte, data []byte) error {
	if reg == ctrlMeasAddr && b.modeReads > 0 {
		b.modeReads--
		if b.modeReads == 0 {
			b.registers[ctrlMeasAddr] &^= 3
		}
	}
	return b.registerBus.ReadReg(reg, data)
}

func (b *conversionBus) WriteReg(reg byte, data []byte) error {
	if reg == ctrlMeasAddr && data[0]&3 == byte(ModeForced) && b.registers[ctrlMeasAddr]&3 != byte(ModeSleep) {
		b.overlapped = true
	}
	return b.registerBus.WriteReg(reg, data)
}

func TestReadWaitsForActiveConversion(t *testing.T) {
	for _, tt := range []struct {
		name      string
		modeReads int
		failRead  int
		wantError bool
	}{
		{"completes", 3, 0, false},
		{"stuck", 0, 0, true},
		{"mode read fails", 3, 2, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b := &conversionBus{registerBus: calibratedBus()}
			d := New(b)
			if err := d.InitWith(ModeForced, Settings{
				TemperatureOversampling: Oversampling1x,
				PressureOversampling:    Oversampling1x,
				HumidityOversampling:    Oversampling1x,
			}); err != nil {
				t.Fatal(err)
			}
			copy(b.registers[dataAddr:], []byte{0x65, 0x5A, 0xC0, 0x7E, 0xED, 0, 0x75, 0x30})
			b.modeReads, b.reads, b.failRead = tt.modeReads, 0, tt.failRead
			b.err = errors.New("mode read failed")
			writes := b.writes
			got, err := d.Read()
			if (err != nil) != tt.wantError || b.overlapped {
				t.Fatalf("Read error=%v, overlapped=%v; want error=%v", err, b.overlapped, tt.wantError)
			}
			if tt.wantError && (b.writes != writes || got != (Response{})) {
				t.Fatal("failed wait triggered a conversion or returned a sample")
			}
			if tt.failRead != 0 && err != b.err {
				t.Fatalf("error=%v, want %v", err, b.err)
			}
			if !tt.wantError && (b.writes-writes != 2 || math.Abs(got.Temperature-25.08) > 0.01) {
				t.Fatalf("Read did not trigger and read a new conversion: %+v", got)
			}
		})
	}
}

func TestHumidityCalibration(t *testing.T) {
	for _, tt := range []struct {
		name   string
		h4, h5 int16
		raw    uint32
	}{
		{"typical", 325, 50, 30000},
		{"large H5", 325, 300, 30000},
		{"negative H4", -30, 50, 1000},
		{"negative H5", 325, -300, 30000},
		{"saturation", 325, 50, 65535},
		{"dry", 325, 50, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b := &registerBus{}
			b.registers[0xA1] = 75
			binary.LittleEndian.PutUint16(b.registers[humidityCalibDataAddr:], 362)
			b.registers[0xE4] = byte(tt.h4 >> 4)
			b.registers[0xE5] = byte(tt.h4&15) | byte(tt.h5&15)<<4
			b.registers[0xE6] = byte(tt.h5 >> 4)
			b.registers[0xE7] = 30
			d := New(b)
			if err := d.readCalibData(); err != nil {
				t.Fatal(err)
			}
			if d.calib.h4 != tt.h4 || d.calib.h5 != tt.h5 {
				t.Fatalf("H4/H5 = %d/%d, want %d/%d", d.calib.h4, d.calib.h5, tt.h4, tt.h5)
			}
			d.calib.tFine = 128422
			// Independent floating-point Bosch reference; H3 is zero in this fixture.
			v := 128422.0 - 76800
			want := (float64(tt.raw) - (float64(tt.h4)*64 + float64(tt.h5)/16384*v)) * (362 / 65536.0 * (1 + 30/67108864.0*v))
			want *= 1 - 75*want/524288.0
			want = math.Max(0, math.Min(100, want))
			if got := d.compensateHumidity(tt.raw); math.Abs(got-want) > 0.15 {
				t.Fatalf("humidity = %v, want %v", got, want)
			}
		})
	}
}
