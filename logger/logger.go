package logger

import (
	"log"
	"os"
	"strconv"
)

type Level int

const (
	DebugLevel Level = iota
	InfoLevel
	WarnLevel
	ErrorLevel
)

type Logger struct {
	component string
	level     Level
}

var globalLevel Level = InfoLevel

func Init() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.Lshortfile)

	globalLevel = InfoLevel

	if v := os.Getenv("DEBUG"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed == 1 {
			globalLevel = DebugLevel
		}
	}
}

func Module(component string) *Logger {
	return &Logger{
		component: component,
		level:     globalLevel,
	}
}

func (l *Logger) SetLevel(level Level) *Logger {
	l.level = level
	return l
}

func (l *Logger) enabled(level Level) bool {
	return level >= l.level
}

func (l *Logger) Debug(format string, v ...any) {
	if !l.enabled(DebugLevel) {
		return
	}
	log.Printf("[DEBUG] "+l.component+": "+format, v...)
}

func (l *Logger) Info(format string, v ...any) {
	if !l.enabled(InfoLevel) {
		return
	}
	log.Printf("[INFO] "+l.component+": "+format, v...)
}

func (l *Logger) Warn(format string, v ...any) {
	if !l.enabled(WarnLevel) {
		return
	}
	log.Printf("[WARN] "+l.component+": "+format, v...)
}

func (l *Logger) Error(format string, v ...any) {
	if !l.enabled(ErrorLevel) {
		return
	}
	log.Printf("[ERROR] "+l.component+": "+format, v...)
}

func (l *Logger) Fatal(format string, v ...any) {
	log.Printf("[FATAL] "+l.component+": "+format, v...)
	os.Exit(1)
}
