package emulator

import "slices"

const (
	MaxGap      = 16
	MaxReadSize = 4096
)

type tempWatch struct {
	Spec ReadSpec
	Addr int
	Size int
}

func CompileReadPlan(
	plan *ReadPlan,
	resolve func(*ReadPlan, ReadSpec) int,
	mapper AddressMapper,
) *CompiledReadPlan {
	tmp := make([]tempWatch, 0, len(plan.Watches))

	for _, spec := range plan.Watches {
		addr := resolve(plan, spec)

		if mapper != nil {
			addr = mapper(plan, spec, addr)
		}

		size := spec.SizeOverride
		if size == 0 {
			size = spec.Size()
		}

		tmp = append(tmp, tempWatch{
			Spec: spec,
			Addr: addr,
			Size: size,
		})
	}

	slices.SortFunc(tmp, func(a, b tempWatch) int {
		return a.Addr - b.Addr
	})

	out := &CompiledReadPlan{}

	for _, w := range tmp {
		if len(out.Regions) == 0 {
			out.Regions = append(out.Regions, MergedRegion{
				Bank:  w.Spec.Bank,
				Start: w.Addr,
				Size:  w.Size,
			})
		}

		cur := &out.Regions[len(out.Regions)-1]

		curEnd := cur.Start + cur.Size
		wEnd := w.Addr + w.Size

		canMerge :=
			w.Addr <= curEnd+MaxGap &&
				(wEnd-cur.Start) <= MaxReadSize

		if !canMerge {
			out.Regions = append(out.Regions, MergedRegion{
				Bank:  w.Spec.Bank,
				Start: w.Addr,
				Size:  w.Size,
			})

			cur = &out.Regions[len(out.Regions)-1]
		} else if wEnd > curEnd {
			cur.Size = wEnd - cur.Start
		}

		cur.Watches = append(cur.Watches, ResolvedWatch{
			Spec:   w.Spec,
			Addr:   w.Addr,
			Size:   w.Size,
			Offset: w.Addr - cur.Start,
		})
	}

	for i := range out.Regions {
		out.Regions[i].Buffer = make([]byte, out.Regions[i].Size)
	}

	return out
}
