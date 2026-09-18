package niimbot

import "fmt"

// Request command codes (host -> printer). Names follow niimbluelib.
const (
	CmdPrintStart     byte = 0x01
	CmdPageStart      byte = 0x03
	CmdSetPageSize    byte = 0x13
	CmdPrintQuantity  byte = 0x15
	CmdRfidInfo       byte = 0x1A
	CmdPrintClear     byte = 0x20
	CmdSetDensity     byte = 0x21
	CmdSetLabelType   byte = 0x23
	CmdPrinterInfo    byte = 0x40
	CmdPrintTestPage  byte = 0x5A
	CmdBitmapRowIndex byte = 0x83 // one-way: row with <=6 black pixels as indexes
	CmdEmptyRow       byte = 0x84 // one-way
	CmdBitmapRow      byte = 0x85 // one-way
	CmdCheckLine      byte = 0x86 // flow-control marker every 200 rows (B21 V1)
	CmdPrintStatus    byte = 0xA3
	CmdPrinterStatus  byte = 0xA5
	CmdConnect        byte = 0xC1 // official app's first packet; prefixed by 0x03
	CmdCancelPrint    byte = 0xDA
	CmdHeartbeat      byte = 0xDC
	CmdPageEnd        byte = 0xE3
	CmdPrintEnd       byte = 0xF3
)

// Response command codes (printer -> host).
const (
	RespNotSupported  byte = 0x00
	RespPrintStart    byte = 0x02
	RespPageStart     byte = 0x04
	RespSetPageSize   byte = 0x14
	RespPrintQuantity byte = 0x16
	RespRfidInfo      byte = 0x1B
	RespPrintClear    byte = 0x30
	RespSetDensity    byte = 0x31
	RespSetLabelType  byte = 0x33
	RespPrintTestPage byte = 0x6A
	RespPrintStatus   byte = 0xB3
	RespPrinterStatus byte = 0xB5
	RespConnect       byte = 0xC2
	RespResetTimeout  byte = 0xC6 // unsolicited, precedes RespCheckLine
	RespCancelPrint   byte = 0xD0
	RespCheckLine     byte = 0xD3 // unsolicited on some models after PageEnd
	RespHeartbeatAdv2 byte = 0xD9
	RespPrintError    byte = 0xDB
	RespHeartbeatAdv1 byte = 0xDD
	RespHeartbeatBase byte = 0xDE
	RespHeartbeatInfo byte = 0xDF
	RespPageIndex     byte = 0xE0
	RespPageEnd       byte = 0xE4
	RespPrintEnd      byte = 0xF4
)

// Printer-info keys for CmdPrinterInfo. The response command is 0x40 + key.
type InfoKey byte

// Printer info keys.
const (
	InfoDensity      InfoKey = 1
	InfoPrintSpeed   InfoKey = 2
	InfoLabelType    InfoKey = 3
	InfoLanguage     InfoKey = 6
	InfoAutoShutdown InfoKey = 7
	InfoDeviceType   InfoKey = 8
	InfoSoftVersion  InfoKey = 9
	InfoBattery      InfoKey = 10
	InfoSerial       InfoKey = 11
	InfoHardVersion  InfoKey = 12
	InfoBTAddress    InfoKey = 13
)

// LabelType selects the media the printer expects (CmdSetLabelType).
type LabelType byte

// Label types.
const (
	LabelGap          LabelType = 1 // die-cut labels with gaps
	LabelBlackMark    LabelType = 2
	LabelContinuous   LabelType = 3
	LabelTransparent  LabelType = 5
	LabelBlackMarkGap LabelType = 10
)

// ErrorCode is a printer-reported error (RespPrintError data[0], or the error
// field of a long RespPrintStatus).
type ErrorCode byte

// Printer error codes (names from niimbluelib).
const (
	ErrCoverOpen          ErrorCode = 0x01
	ErrNoPaper            ErrorCode = 0x02
	ErrLowBattery         ErrorCode = 0x03
	ErrBatteryException   ErrorCode = 0x04
	ErrUserCancel         ErrorCode = 0x05
	ErrDataError          ErrorCode = 0x06
	ErrOverheat           ErrorCode = 0x07
	ErrPaperOutException  ErrorCode = 0x08
	ErrPrinterBusy        ErrorCode = 0x09
	ErrNoPrintHead        ErrorCode = 0x0A
	ErrTemperatureLow     ErrorCode = 0x0B
	ErrPrintHeadLoose     ErrorCode = 0x0C
	ErrNoRibbon           ErrorCode = 0x0D
	ErrWrongRibbon        ErrorCode = 0x0E
	ErrUsedRibbon         ErrorCode = 0x0F
	ErrWrongPaper         ErrorCode = 0x10
	ErrSetPaperFail       ErrorCode = 0x11
	ErrSetPrintModeFail   ErrorCode = 0x12
	ErrSetDensityFail     ErrorCode = 0x13
	ErrWriteRfidFail      ErrorCode = 0x14
	ErrSetMarginFail      ErrorCode = 0x15
	ErrCommunication      ErrorCode = 0x16
	ErrDisconnect         ErrorCode = 0x17
	ErrCanvasParameter    ErrorCode = 0x18
	ErrRotationParameter  ErrorCode = 0x19
	ErrJsonParameter      ErrorCode = 0x1A
	ErrIllegalPage        ErrorCode = 0x32
	ErrReceiveDataTimeout ErrorCode = 0x34
)

var errorNames = map[ErrorCode]string{
	ErrCoverOpen: "cover open", ErrNoPaper: "no paper", ErrLowBattery: "low battery",
	ErrBatteryException: "battery exception", ErrUserCancel: "cancelled by user",
	ErrDataError: "data error", ErrOverheat: "print head overheated",
	ErrPaperOutException: "paper out exception", ErrPrinterBusy: "printer busy",
	ErrNoPrintHead: "no print head", ErrTemperatureLow: "temperature too low",
	ErrPrintHeadLoose: "print head loose", ErrNoRibbon: "no ribbon",
	ErrWrongRibbon: "wrong ribbon", ErrUsedRibbon: "used ribbon", ErrWrongPaper: "wrong paper",
	ErrSetPaperFail: "set paper failed", ErrSetPrintModeFail: "set print mode failed",
	ErrSetDensityFail: "set density failed", ErrWriteRfidFail: "write RFID failed",
	ErrSetMarginFail: "set margin failed", ErrCommunication: "communication exception",
	ErrDisconnect: "disconnected", ErrCanvasParameter: "canvas parameter error",
	ErrRotationParameter: "rotation parameter error", ErrJsonParameter: "json parameter error",
	ErrIllegalPage: "illegal page", ErrReceiveDataTimeout: "receive data timeout",
}

// String returns a human-readable name for the error code.
func (e ErrorCode) String() string {
	if s, ok := errorNames[e]; ok {
		return s
	}
	return fmt.Sprintf("printer error %#02x", byte(e))
}

// PrintError is returned when the printer reports an error (RespPrintError).
type PrintError struct{ Code ErrorCode }

func (e *PrintError) Error() string { return "niimbot: " + e.Code.String() }
