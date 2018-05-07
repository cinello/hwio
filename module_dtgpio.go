// A GPIO module that uses Linux 3.7+ file system drivers with device tree. This module is intended to work for any 3.7+ configuration,
// including BeagleBone Black and Raspberry Pi's with new kernels. The actual pin configuration is passed through on SetOptions.

package hwio

import (
	"errors"
	"fmt"
	"os"
	"strconv"
)

type DTGPIOModule struct {
	name        string
	definedPins DTGPIOModulePinDefMap
	openPins    map[Pin]*DTGPIOModuleOpenPin
}

// Represents the definition of a GPIO pin, which should contain all the info required to open, close, read and write the pin
// using FS drivers.
type DTGPIOModulePinDef struct {
	pin         Pin
	gpioLogical int
}

// A map of GPIO pin definitions.
type DTGPIOModulePinDefMap map[Pin]*DTGPIOModulePinDef

type DTGPIOModuleOpenPin struct {
	pin          Pin
	gpioLogical  int
	gpioBaseName string
	mode         PinIOMode
	valueFile    *os.File
}

func NewDTGPIOModule(name string) (result *DTGPIOModule) {
	result = &DTGPIOModule{name: name}
	result.openPins = make(map[Pin]*DTGPIOModuleOpenPin)
	return result
}

// Set options of the module. Parameters we look for include:
// - "pins" - an object of type DTGPIOModulePinDefMap
func (module *DTGPIOModule) SetOptions(options map[string]interface{}) error {
	v := options["pins"]
	if v == nil {
		return fmt.Errorf("module '%s' SetOptions() did not get 'pins' values", module.GetName())
	}

	module.definedPins = v.(DTGPIOModulePinDefMap)
	return nil
}

// enable GPIO module. It doesn't allocate any pins immediately.
func (module *DTGPIOModule) Enable() error {
	return nil
}

// disables module and release any pins assigned.
func (module *DTGPIOModule) Disable() error {
	for _, openPin := range module.openPins {
		openPin.gpioUnexport()
	}
	return nil
}

func (module *DTGPIOModule) GetName() string {
	return module.name
}

func (module *DTGPIOModule) PinMode(pin Pin, mode PinIOMode) error {
	if module.definedPins[pin] == nil {
		return fmt.Errorf("pin %d is not known as a GPIO pin", pin)
	}

	// close if already open and the new mode in different
	if oldOpenPin, ok := module.openPins[pin]; ok && mode != oldOpenPin.mode {
		ClosePin(pin)
	}

	// attempt to assign this pin for this module.
	e := AssignPin(pin, module)
	if e != nil {
		return e
	}

	// Create an open pin object
	openPin, e := module.makeOpenGPIOPin(pin)
	if e != nil {
		return e
	}

	e = openPin.gpioExport()
	if e != nil {
		return e
	}

	if mode == Output {
		e = openPin.gpioDirection("out")
		if e != nil {
			return e
		}
	} else {
		e = openPin.gpioDirection("in")
		// @todo implement pull up and pull down support

		// pull := BB_CONF_PULL_DISABLE
		// // note: pull up/down modes assume that CONF_PULLDOWN resets the pull disable bit
		// if mode == InputPullUp {
		// 	pull = BB_CONF_PULLUP
		// } else if mode == InputPullDown {
		// 	pull = BB_CONF_PULLDOWN
		// }

		if e != nil {
			return e
		}
	}
	openPin.mode = mode
	return nil
}

func (module *DTGPIOModule) DigitalWrite(pin Pin, value int) (e error) {
	openPin := module.openPins[pin]
	if openPin == nil {
		return errors.New("pin is being written but has not been opened, called PinMode")
	}
	// 	if a.pinIOMode != Output {
	// 		return errors.New(fmt.Sprintf("DigitalWrite: pin %d mode is not set for output", pin))
	// 	}
	openPin.gpioSetValue(value)
	return nil
}

func (module *DTGPIOModule) DigitalRead(pin Pin) (value int, e error) {
	openPin := module.openPins[pin]
	if openPin == nil {
		return 0, errors.New("pin is being read from but has not been opened, call PinMode")
	}
	// 	if a.pinIOMode != Input && a.pinIOMode != InputPullUp && a.pinIOMode != InputPullDown {
	// 		e = errors.New(fmt.Sprintf("DigitalRead: pin %d mode not set for input", pin))
	// 		return
	// 	}

	return openPin.gpioGetValue()
}

func (module *DTGPIOModule) ClosePin(pin Pin) error {
	openPin := module.openPins[pin]
	if openPin == nil {
		return errors.New("pin is being closed but has not been opened, call PinMode")
	}
	e := openPin.gpioUnexport()
	if e != nil {
		return e
	}
	e = openPin.valueFile.Close()
	if e != nil {
		return e
	}
	delete(module.openPins, pin)
	return UnassignPin(pin)
}

// create an openPin object and put it in the map.
func (module *DTGPIOModule) makeOpenGPIOPin(pin Pin) (*DTGPIOModuleOpenPin, error) {
	p := module.definedPins[pin]
	if p == nil {
		return nil, fmt.Errorf("pin %d is not known to GPIO module", pin)
	}

	result := &DTGPIOModuleOpenPin{pin: pin, gpioLogical: p.gpioLogical}
	module.openPins[pin] = result

	return result, nil
}

// For GPIO:
// - write GPIO pin to /sys/class/gpio/export. This is the port number plus pin on that port. Ports 0, 32, 64, 96. In our case, gpioLogical
//   contains this value.
// - write direction to /sys/class/gpio/gpio{nn}/direction. Values are 'in' and 'out'

// Needs to be called to allocate the GPIO pin
func (op *DTGPIOModuleOpenPin) gpioExport() error {
	bn := "/sys/class/gpio/gpio" + strconv.Itoa(op.gpioLogical)
	if !fileExists(bn) {
		s := strconv.FormatInt(int64(op.gpioLogical), 10)
		e := WriteStringToFile("/sys/class/gpio/export", s)
		if e != nil {
			return e
		}
	}

	// calculate the base name for the gpio pin
	op.gpioBaseName = bn
	return nil
}

// Needs to be called to allocate the GPIO pin
func (op *DTGPIOModuleOpenPin) gpioUnexport() error {
	s := strconv.FormatInt(int64(op.gpioLogical), 10)
	e := WriteStringToFile("/sys/class/gpio/unexport", s)
	if e != nil {
		return e
	}

	return nil
}

// Once exported, the direction of a GPIO can be set
func (op *DTGPIOModuleOpenPin) gpioDirection(dir string) error {
	if dir != "in" && dir != "out" {
		return errors.New("direction must be in or out")
	}
	f := op.gpioBaseName + "/direction"
	e := WriteStringToFile(f, dir)

	mode := os.O_WRONLY | os.O_TRUNC
	if dir == "in" {
		mode = os.O_RDONLY
	}

	// open the value file with the correct mode. Put that file in 'op'. Note that we keep this file open
	// continuously for performance.
	// Preliminary tests on 200,000 DigitalWrites indicate an order of magnitude improvement when we don't have
	// to re-open the file each time. Re-seeking and writing a new value suffices.
	op.valueFile, e = os.OpenFile(op.gpioBaseName+"/value", mode, 0666)

	return e
}

// Get the value. Will return High or Low
func (op *DTGPIOModuleOpenPin) gpioGetValue() (int, error) {
	var b []byte
	b = make([]byte, 1)
	n, e := op.valueFile.ReadAt(b, 0)

	value := 0
	if n > 0 {
		if b[0] == '1' {
			value = High
		} else {
			value = Low
		}
	}
	return value, e
}

// Set the value, Expects High or Low
func (op *DTGPIOModuleOpenPin) gpioSetValue(value int) error {
	if op.valueFile == nil {
		return errors.New("value file is not defined")
	}

	// Seek the start of the value file before writing. This is sufficient for the driver to accept a new value.
	_, e := op.valueFile.Seek(0, 0)
	if e != nil {
		return e
	}

	// Write a 1 or 0.
	// @todo investigate if we'd get better performance if we have precalculated []byte values with 0 and 1, and
	// use write directly instead of WriteString. Probably only marginal.
	// @todo also check out http://hackaday.com/2013/12/07/speeding-up-beaglebone-black-gpio-a-thousand-times/
	if value == 0 {
		op.valueFile.WriteString("0")
	} else {
		op.valueFile.WriteString("1")
	}

	return nil
}
