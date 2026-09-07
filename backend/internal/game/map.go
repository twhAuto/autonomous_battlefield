// Implements odd-row hex coordinates, map validation, reachability, and deterministic
// pathfinding for the simulation.

package game

import (
	"container/heap"
	"fmt"
	"sort"
)

func (m Map) InBounds(position Position) bool {
	return position.X >= 0 && position.X < m.Width && position.Y >= 0 && position.Y < m.Height
}

func (m Map) TerrainAt(position Position) string {
	if !m.InBounds(position) || position.Y >= len(m.Rows) || position.X >= len(m.Rows[position.Y]) {
		return ""
	}
	switch m.Rows[position.Y][position.X] {
	case '.':
		return TerrainGround
	case '#':
		return TerrainRock
	case 'b':
		return TerrainBuildSite
	default:
		return ""
	}
}

func (m Map) Traversable(position Position) bool { return m.TerrainAt(position) == TerrainGround }

func Neighbors(position Position) []Position {
	var offsets [6]Position
	if position.Y%2 == 0 {
		offsets = [6]Position{{X: -1, Y: -1}, {X: 0, Y: -1}, {X: -1}, {X: 1}, {X: -1, Y: 1}, {X: 0, Y: 1}}
	} else {
		offsets = [6]Position{{X: 0, Y: -1}, {X: 1, Y: -1}, {X: -1}, {X: 1}, {X: 0, Y: 1}, {X: 1, Y: 1}}
	}
	result := make([]Position, 0, 6)
	for _, offset := range offsets {
		result = append(result, Position{X: position.X + offset.X, Y: position.Y + offset.Y})
	}
	sortPositions(result)
	return result
}

func HexDistance(left, right Position) int {
	lq, lr := offsetToAxial(left)
	rq, rr := offsetToAxial(right)
	return (abs(lq-rq) + abs(lq+lr-rq-rr) + abs(lr-rr)) / 2
}

func offsetToAxial(position Position) (int, int) {
	return position.X - (position.Y-(position.Y&1))/2, position.Y
}

func ValidateMap(value Map) error {
	buildSites, err := validateMapTerrain(value)
	if err != nil {
		return err
	}
	seen := make(map[Position]string)
	if err := validateCastleSpawns(value, seen); err != nil {
		return err
	}
	if err := validateEconomySites(value, buildSites, seen); err != nil {
		return err
	}
	return validateMapSymmetryAndReachability(value)
}

func validateMapTerrain(value Map) (map[Position]bool, error) {
	if value.Kind != "authored" {
		return nil, fmt.Errorf("%w: map kind must be authored", ErrInvalidScenario)
	}
	if value.Width < 15 || value.Width > 41 || value.Width%2 == 0 || value.Height < 11 || value.Height > 31 || value.Height%2 == 0 || len(value.Rows) != value.Height {
		return nil, fmt.Errorf("%w: authored map dimensions must be odd, width 15..41 and height 11..31", ErrInvalidScenario)
	}
	buildSites := make(map[Position]bool)
	for y, row := range value.Rows {
		if len(row) != value.Width {
			return nil, fmt.Errorf("%w: map row %d has width %d", ErrInvalidScenario, y, len(row))
		}
		for x := range row {
			terrain := value.TerrainAt(Position{X: x, Y: y})
			if terrain == "" {
				return nil, fmt.Errorf("%w: unsupported terrain at (%d,%d)", ErrInvalidScenario, x, y)
			}
			if terrain == TerrainBuildSite {
				buildSites[Position{X: x, Y: y}] = true
			}
		}
	}
	return buildSites, nil
}

func validateCastleSpawns(value Map, seen map[Position]string) error {
	for slot, spawn := range value.CastleSpawns {
		if !value.Traversable(spawn) {
			return fmt.Errorf("%w: castle spawn %d is not traversable", ErrInvalidScenario, slot)
		}
		if previous := seen[spawn]; previous != "" {
			return fmt.Errorf("%w: duplicate %s and castle spawn", ErrInvalidScenario, previous)
		}
		seen[spawn] = "castle spawn"
		freeNeighbors := 0
		for _, neighbor := range Neighbors(spawn) {
			if value.InBounds(neighbor) && value.Traversable(neighbor) {
				freeNeighbors++
			}
		}
		if freeNeighbors < 4 {
			return fmt.Errorf("%w: castle spawn %d needs four adjacent ground tiles", ErrInvalidScenario, slot)
		}
	}
	return nil
}

func validateEconomySites(value Map, buildSites map[Position]bool, seen map[Position]string) error {
	for slot, sites := range value.EconomySites {
		if len(sites) < 2 || len(sites) > 8 || len(sites) != len(value.EconomySites[1-slot]) {
			return fmt.Errorf("%w: slot %d requires two to eight paired construction sites", ErrInvalidScenario, slot)
		}
		for _, site := range sites {
			if !buildSites[site] {
				return fmt.Errorf("%w: economy site (%d,%d) is not marked b", ErrInvalidScenario, site.X, site.Y)
			}
			if previous := seen[site]; previous != "" {
				return fmt.Errorf("%w: duplicate %s and economy site", ErrInvalidScenario, previous)
			}
			seen[site] = "economy site"
		}
	}
	return nil
}

func validateMapSymmetryAndReachability(value Map) error {
	for y, row := range value.Rows {
		for x := range row {
			mirror := value.Width - 1 - x
			if row[x] != row[mirror] {
				return fmt.Errorf("%w: terrain is not horizontally symmetric at (%d,%d)", ErrInvalidScenario, x, y)
			}
		}
	}
	if mirrorPosition(value.CastleSpawns[0], value.Width) != value.CastleSpawns[1] {
		return fmt.Errorf("%w: castle spawns are not symmetric", ErrInvalidScenario)
	}
	for index, site := range value.EconomySites[0] {
		if mirrorPosition(site, value.Width) != value.EconomySites[1][index] {
			return fmt.Errorf("%w: economy sites are not symmetric", ErrInvalidScenario)
		}
	}
	if !reachable(value, value.CastleSpawns[0], value.CastleSpawns[1]) {
		return fmt.Errorf("%w: castle spawns are disconnected", ErrInvalidScenario)
	}
	return nil
}

func mirrorPosition(position Position, width int) Position {
	return Position{X: width - 1 - position.X, Y: position.Y}
}

func reachable(m Map, start, destination Position) bool {
	visited := map[Position]bool{start: true}
	queue := []Position{start}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current == destination {
			return true
		}
		for _, next := range Neighbors(current) {
			if !visited[next] && m.InBounds(next) && m.Traversable(next) {
				visited[next] = true
				queue = append(queue, next)
			}
		}
	}
	return false
}

type pathNode struct {
	position Position
	g, f     int
	index    int
}

type pathQueue []*pathNode

type pathProgress struct {
	cameFrom map[Position]Position
	best     map[Position]int
	open     *pathQueue
}

func (q pathQueue) Len() int { return len(q) }
func (q pathQueue) Less(i, j int) bool {
	if q[i].f != q[j].f {
		return q[i].f < q[j].f
	}
	if q[i].g != q[j].g {
		return q[i].g < q[j].g
	}
	if q[i].position.Y != q[j].position.Y {
		return q[i].position.Y < q[j].position.Y
	}
	return q[i].position.X < q[j].position.X
}
func (q pathQueue) Swap(i, j int) { q[i], q[j] = q[j], q[i]; q[i].index = i; q[j].index = j }
func (q *pathQueue) Push(value any) {
	node := value.(*pathNode)
	node.index = len(*q)
	*q = append(*q, node)
}
func (q *pathQueue) Pop() any { old := *q; node := old[len(old)-1]; *q = old[:len(old)-1]; return node }

func shortestPath(m Map, start Position, goals map[Position]bool, occupied map[Position]bool) []Position {
	if goals[start] {
		return nil
	}
	open := &pathQueue{}
	heap.Init(open)
	heap.Push(open, &pathNode{position: start, f: nearestDistance(start, goals)})
	progress := &pathProgress{cameFrom: make(map[Position]Position), best: map[Position]int{start: 0}, open: open}
	closed := make(map[Position]bool)
	for open.Len() > 0 {
		current := heap.Pop(open).(*pathNode)
		if closed[current.position] {
			continue
		}
		closed[current.position] = true
		if goals[current.position] {
			return reconstructPath(start, current.position, progress.cameFrom)
		}
		expandPathFrontier(m, current, goals, occupied, closed, progress)
	}
	return nil
}

func reconstructPath(start, destination Position, cameFrom map[Position]Position) []Position {
	path := []Position{destination}
	for path[len(path)-1] != start {
		path = append(path, cameFrom[path[len(path)-1]])
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path[1:]
}

func expandPathFrontier(m Map, current *pathNode, goals, occupied, closed map[Position]bool, progress *pathProgress) {
	for _, next := range Neighbors(current.position) {
		if !pathStepAvailable(m, next, occupied, closed) {
			continue
		}
		candidate := current.g + 1
		if previous, ok := progress.best[next]; ok && candidate >= previous {
			continue
		}
		progress.best[next] = candidate
		progress.cameFrom[next] = current.position
		heap.Push(progress.open, &pathNode{position: next, g: candidate, f: candidate + nearestDistance(next, goals)})
	}
}

func pathStepAvailable(m Map, position Position, occupied, closed map[Position]bool) bool {
	return m.InBounds(position) && m.Traversable(position) && !occupied[position] && !closed[position]
}

func nearestDistance(position Position, goals map[Position]bool) int {
	best := int(^uint(0) >> 1)
	for goal := range goals {
		if distance := HexDistance(position, goal); distance < best {
			best = distance
		}
	}
	if best == int(^uint(0)>>1) {
		return 0
	}
	return best
}

func sortPositions(values []Position) {
	sort.Slice(values, func(i, j int) bool {
		if values[i].Y != values[j].Y {
			return values[i].Y < values[j].Y
		}
		return values[i].X < values[j].X
	})
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
