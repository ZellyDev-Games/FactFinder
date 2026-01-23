package emulator

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Bank string

const (
	WRAM Bank = "wram"
	SRAM Bank = "sram"
)

type HexInt int

func (h *HexInt) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.ScalarNode {
		return fmt.Errorf("expected scalar for hex int")
	}

	s := strings.TrimSpace(value.Value)
	s = strings.TrimPrefix(strings.ToLower(s), "0x")

	v, err := strconv.ParseInt(s, 16, 0)
	if err != nil {
		return err
	}

	*h = HexInt(v)
	return nil
}

type Signal string

const (
	Rising  Signal = "rising"
	Falling Signal = "falling"
	Delta   Signal = "delta"
	Edge    Signal = "edge"
)

type ReadSpec struct {
	Name    string    `yaml:"name"`
	Address HexInt    `yaml:"address"`
	Type    ValueType `yaml:"type"`
	Bank    Bank      `yaml:"bank"`
	Signals []Signal  `yaml:"signals"`
}

func (r ReadSpec) Size() int {
	switch r.Type {
	case I8, U8, Bool:
		return 1
	case I16, U16:
		return 2
	case I32, U32:
		return 4
	case I64, U64:
		return 8
	default:
		return 0
	}
}

type ReadPlan struct {
	Name         string     `yaml:"Name"`
	ReadInterval int64      `yaml:"ReadInterval"`
	HiROM        bool       `yaml:"HiROM"`
	Watches      []ReadSpec `yaml:"Watches"`
	Platform     string     `yaml:"Platform"`
}

func NewReadPlan(reader io.Reader) (*ReadPlan, error) {
	rp := ReadPlan{}
	rawYaml, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}

	err = yaml.Unmarshal(rawYaml, &rp)
	if err != nil {
		return nil, err
	}

	return &rp, nil
}
