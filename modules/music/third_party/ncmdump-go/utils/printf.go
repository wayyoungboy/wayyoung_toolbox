package utils

import (
	"fmt"
	"github.com/TwiN/go-color"
)

func DonePrintfln(format string, a ...interface{}) {
	fmt.Printf(color.InBold(color.InGreen("[Done] "))+format+"\n", a...)
}

func InfoPrintfln(format string, a ...interface{}) {
	fmt.Printf(color.InBold(color.InBlue("[Info] "))+format+"\n", a...)
}

func WarningPrintfln(format string, a ...interface{}) {
	fmt.Printf(color.InBold(color.InYellow("[Warning] "))+format+"\n", a...)
}

func ErrorPrintfln(format string, a ...interface{}) {
	fmt.Printf(color.InBold(color.InRed("[Error] "))+format+"\n", a...)
}
