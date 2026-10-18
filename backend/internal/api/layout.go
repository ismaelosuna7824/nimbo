package api

import (
	"math"
	"sort"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Canvas card size and spacing; they match the desktop canvas
// (apps/desktop/src/features/canvas/layout.ts NODE_WIDTH/NODE_HEIGHT).
const (
	cardWidth  = 248.0
	cardHeight = 84.0
	cardGap    = 24.0
)

// settleNewNodes moves every node not in before whose card overlaps another
// card to the nearest free grid slot around its proposed position. Catalog
// places curated helpers between source and target and starters at the drop
// point, so this is the single place that keeps cards from stacking for every
// client (UI, MCP). Groups (nodes other nodes name as parent) are drawn
// around their children and unpositioned nodes are laid out by the UI, so
// neither takes part. Nodes are processed in workflow order, which keeps the
// result deterministic.
func settleNewNodes(wf *model.Workflow, before map[string]bool) {
	ids := nodeIDs(wf)
	groups := map[string]bool{}
	for _, n := range wf.Nodes {
		if n.Parent != "" && n.Parent != n.ID && ids[n.Parent] {
			groups[n.Parent] = true
		}
	}
	placeable := func(n *model.Node) bool { return n.Positioned && !groups[n.ID] }

	var occupied []model.Position
	for i := range wf.Nodes {
		if n := &wf.Nodes[i]; before[n.ID] && placeable(n) {
			occupied = append(occupied, n.Position)
		}
	}
	for i := range wf.Nodes {
		n := &wf.Nodes[i]
		if before[n.ID] || !placeable(n) {
			continue
		}
		n.Position = freeSlot(n.Position, occupied)
		occupied = append(occupied, n.Position)
	}
}

func overlapsAny(p model.Position, occupied []model.Position) bool {
	for _, o := range occupied {
		if math.Abs(o.X-p.X) < cardWidth+cardGap && math.Abs(o.Y-p.Y) < cardHeight+cardGap {
			return true
		}
	}
	return false
}

// freeSlot returns the free grid slot (one card plus gap apart) nearest to
// p, ties broken by row then column.
func freeSlot(p model.Position, occupied []model.Position) model.Position {
	if !overlapsAny(p, occupied) {
		return p
	}
	const dx, dy = cardWidth + cardGap, cardHeight + cardGap
	type slot struct {
		i, j int
		d    float64
	}
	// Slots within the square of radius r, nearest first. Any slot outside
	// it is at least (r+1)*min(dx, dy) away, so a free slot closer than that
	// is the global nearest.
	for r := 1; ; r++ {
		var square []slot
		for j := -r; j <= r; j++ {
			for i := -r; i <= r; i++ {
				x, y := float64(i)*dx, float64(j)*dy
				square = append(square, slot{i, j, math.Hypot(x, y)})
			}
		}
		sort.Slice(square, func(a, b int) bool {
			if square[a].d != square[b].d {
				return square[a].d < square[b].d
			}
			if square[a].j != square[b].j {
				return square[a].j < square[b].j
			}
			return square[a].i < square[b].i
		})
		bound := float64(r+1) * min(dx, dy)
		for _, s := range square {
			if s.d >= bound {
				break
			}
			c := model.Position{X: p.X + float64(s.i)*dx, Y: p.Y + float64(s.j)*dy}
			if !overlapsAny(c, occupied) {
				return c
			}
		}
	}
}

func nodeIDs(wf *model.Workflow) map[string]bool {
	ids := make(map[string]bool, len(wf.Nodes))
	for _, n := range wf.Nodes {
		ids[n.ID] = true
	}
	return ids
}
