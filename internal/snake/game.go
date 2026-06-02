// Package snake implements a classic Snake game.
package snake

import (
	"math/rand"
	"time"
)

// Direction represents the movement direction of the snake.
type Direction int

const (
	DirUp    Direction = 0
	DirDown  Direction = 1
	DirLeft  Direction = 2
	DirRight Direction = 3
)

// Point represents a coordinate position on the game grid.
type Point struct {
	X, Y int
}

// Game is the main game structure that holds the entire game state.
type Game struct {
	Width      int           // Game area width
	Height     int           // Game area height
	Snake      []Point       // Snake body (index 0 is the head)
	Food       Point         // Food position
	Direction  Direction     // Current movement direction
	NextDir    Direction     // Next movement direction (for buffered input)
	Score      int           // Current score
	GameOver   bool          // Whether the game is over
	Speed      time.Duration // Movement interval
	obstacles  []Point       // Optional obstacles
}

// NewGame creates a new Game with the given width and height.
// The snake starts at the center of the board with length 3,
// moving right by default, and a food item is generated randomly.
func NewGame(width, height int) *Game {
	g := &Game{
		Width:     width,
		Height:    height,
		Speed:     150 * time.Millisecond,
		Direction: DirRight,
		NextDir:   DirRight,
	}
	g.Start()
	return g
}

// Start resets the game to its initial state.
func (g *Game) Start() {
	centerX := g.Width / 2
	centerY := g.Height / 2

	// Initialize snake with length 3, heading right
	g.Snake = []Point{
		{X: centerX, Y: centerY},     // head
		{X: centerX - 1, Y: centerY}, // body
		{X: centerX - 2, Y: centerY}, // tail
	}

	g.Direction = DirRight
	g.NextDir = DirRight
	g.Score = 0
	g.GameOver = false
	g.Speed = 150 * time.Millisecond
	g.obstacles = nil

	g.generateFood()
}

// ChangeDirection changes the snake's movement direction.
// It prevents the snake from turning directly back into itself.
func (g *Game) ChangeDirection(dir Direction) {
	// Prevent reversing direction
	if g.Direction == DirUp && dir == DirDown {
		return
	}
	if g.Direction == DirDown && dir == DirUp {
		return
	}
	if g.Direction == DirLeft && dir == DirRight {
		return
	}
	if g.Direction == DirRight && dir == DirLeft {
		return
	}
	g.NextDir = dir
}

// Tick advances the game by one step: applies buffered direction,
// moves the snake, checks collisions, and handles food consumption.
func (g *Game) Tick() {
	if g.GameOver {
		return
	}

	// Apply buffered direction
	g.Direction = g.NextDir

	// Calculate the new head position
	newHead := g.moveSnake()

	// Add new head
	g.Snake = append([]Point{newHead}, g.Snake...)

	// Check for wall or self collision
	if g.checkCollision() {
		g.GameOver = true
		return
	}

	// Check if snake eats food
	if newHead == g.Food {
		g.Score++
		// Speed up every 5 food items (min 30ms)
		if g.Score%5 == 0 && g.Speed > 30*time.Millisecond {
			g.Speed -= 20 * time.Millisecond
			if g.Speed < 30*time.Millisecond {
				g.Speed = 30 * time.Millisecond
			}
		}
		g.generateFood()
	} else {
		// Remove tail (snake didn't eat)
		g.Snake = g.Snake[:len(g.Snake)-1]
	}
}

// moveSnake calculates the new head position based on the current direction.
func (g *Game) moveSnake() Point {
	head := g.Snake[0]
	var newHead Point

	switch g.Direction {
	case DirUp:
		newHead = Point{X: head.X, Y: head.Y - 1}
	case DirDown:
		newHead = Point{X: head.X, Y: head.Y + 1}
	case DirLeft:
		newHead = Point{X: head.X - 1, Y: head.Y}
	case DirRight:
		newHead = Point{X: head.X + 1, Y: head.Y}
	default:
		newHead = Point{X: head.X + 1, Y: head.Y}
	}

	return newHead
}

// checkCollision checks if the snake head collides with a wall or its own body.
// Returns true if a collision is detected.
func (g *Game) checkCollision() bool {
	head := g.Snake[0]

	// Check wall collision
	if g.IsWall(head.X, head.Y) {
		return true
	}

	// Check self collision (skip head itself at index 0)
	for i := 1; i < len(g.Snake); i++ {
		if g.Snake[i] == head {
			return true
		}
	}

	// Check obstacle collision
	for _, obs := range g.obstacles {
		if obs == head {
			return true
		}
	}

	return false
}

// generateFood places a food item at a random empty position on the board,
// avoiding positions occupied by the snake.
func (g *Game) generateFood() {
	occupied := make(map[Point]bool)
	for _, p := range g.Snake {
		occupied[p] = true
	}
	for _, obs := range g.obstacles {
		occupied[obs] = true
	}

	// Collect all free positions
	var free []Point
	for x := 0; x < g.Width; x++ {
		for y := 0; y < g.Height; y++ {
			if !occupied[Point{X: x, Y: y}] {
				free = append(free, Point{X: x, Y: y})
			}
		}
	}

	// If there are free positions, pick one randomly
	if len(free) > 0 {
		g.Food = free[rand.Intn(len(free))]
	}
}

// IsWall checks whether the given coordinates are outside the game boundary.
func (g *Game) IsWall(x, y int) bool {
	return x < 0 || x >= g.Width || y < 0 || y >= g.Height
}
