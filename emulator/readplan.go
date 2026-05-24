package emulator

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type ResolvedWatch struct {
	Spec   ReadSpec
	Addr   int
	Size   int
	Offset int
}

type MergedRegion struct {
	Bank    Bank
	Start   int
	Size    int
	Watches []ResolvedWatch
	Buffer  []byte
}

type CompiledReadPlan struct {
	Regions []MergedRegion
}

type Bank string

const (
	WRAM  Bank = "wram"  // SNES/GB/GBC Memory
	SRAM  Bank = "sram"  // SNES Save Memory
	RAM   Bank = "ram"   // PSX/NES/Genesis Memory
	IWRAM Bank = "iwram" // GBA Internal Memory
	EWRAM Bank = "ewram" // GBA External Memory
	FCRAM Bank = "fcram" // 3DS Memory
	PSRAM Bank = "psram" // DS Memory
	RDRAM Bank = "rdram" // N64 Memory
)

func (b *Bank) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.ScalarNode {
		return fmt.Errorf("expected scalar for bank")
	}

	switch strings.ToLower(strings.TrimSpace(value.Value)) {
	case "wram":
		*b = WRAM
	case "sram":
		*b = SRAM
	case "ram":
		*b = RAM
	case "iwram":
		*b = IWRAM
	case "ewram":
		*b = EWRAM
	case "fcram":
		*b = FCRAM
	case "psram":
		*b = PSRAM
	case "rdram":
		*b = RDRAM
	default:
		return fmt.Errorf("unknown bank: %q", value.Value)
	}

	return nil
}

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
	Name         string    `yaml:"name"`
	Address      HexInt    `yaml:"address"`
	Type         ValueType `yaml:"type"`
	Bank         Bank      `yaml:"bank,omitempty"`
	SizeOverride int       `yaml:"size,omitempty"`
	Mask         HexInt    `yaml:"mask,omitempty"`
}

func (r ReadSpec) Size() int {
	if r.SizeOverride > 0 {
		return r.SizeOverride
	}

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

	for i := range rp.Watches {
		if rp.Watches[i].Bank == "" {
			switch rp.Platform {
			case "SNES":
				rp.Watches[i].Bank = WRAM
			case "GB":
				rp.Watches[i].Bank = WRAM
			case "GBC":
				rp.Watches[i].Bank = WRAM
			case "PSX":
				rp.Watches[i].Bank = RAM
			case "NES":
				rp.Watches[i].Bank = RAM
			case "Genesis":
				rp.Watches[i].Bank = RAM
			case "GBA":
				rp.Watches[i].Bank = IWRAM
			case "3DS":
				rp.Watches[i].Bank = FCRAM
			case "DS":
				rp.Watches[i].Bank = PSRAM
			case "N64":
				rp.Watches[i].Bank = RDRAM
			}
		}
	}

	return &rp, nil
}
