package emulator

type AddressMapper func(
	plan *ReadPlan,
	spec ReadSpec,
	addr int,
) int

func ResolveAddress(plan *ReadPlan, spec ReadSpec) int {
	switch spec.Bank {

	case WRAM:
		if plan.Platform == "SNES" {
			return int(spec.Address)
		}
		// GB & GBC 0xC000-0xDFFF
		return int(spec.Address)

	case SRAM:
		if plan.HiROM {
			return 0x300000 +
				0x6000 +
				(int(spec.Address) % 0xA000) +
				(int(spec.Address)/0xA000)*0x10000
		}

		return 0x700000 +
			(int(spec.Address) % 0x8000) +
			(int(spec.Address)/0x8000)*0x10000

	case RAM:
		// RAM   Bank = "ram"   // PSX/NES/Genesis Memory
		// NES 0x0000-0x07FF
		// Genesis 0xFF0000-0xFFFFFF
		if plan.Platform != "PSX" {
			return int(spec.Address)
		}
		// PSX 0x010000-0x200000
		return int(spec.Address)

	case IWRAM:
		// IWRAM Bank = "iwram" // GBA Internal Memory
		// GBA 0x19000 – 0x20FFF
		return int(spec.Address)

	case EWRAM:
		// EWRAM Bank = "ewram" // GBA External Memory
		// GBA 0x21000 – 0x60FFF
		return int(spec.Address)

	case FCRAM:
		// FCRAM Bank = "fcram" // 3DS Memory
		// 3DS 0x20000000-0x28000000
		return int(spec.Address)

	case PSRAM:
		// PSRAM Bank = "psram" // DS Memory
		// DeSmuME 0x02000000-0x02400000
		// MelonDS 0x00000000-0x00400000
		return int(spec.Address)

	case RDRAM:
		// RDRAM Bank = "rdram" // N64 Memory
		// 0x00000000 – 0x003FFFFF No expansion pack
		// 0x00000000 – 0x007FFFFF With expansion pack
		return int(spec.Address)
	}

	return 0
}
