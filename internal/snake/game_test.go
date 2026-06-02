package snake

import (
	"testing"
	"time"
)

func TestNewGame(t *testing.T) {
	g := NewGame(10, 10)
	if g.Width != 10 || g.Height != 10 {
		t.Fatalf("expected 10x10, got %dx%d", g.Width, g.Height)
	}
	if len(g.Snake) != 3 {
		t.Fatalf("expected snake length 3, got %d", len(g.Snake))
	}
	if g.GameOver {
		t.Fatal("game should not be over on init")
	}
	if g.Speed != 150*time.Millisecond {
		t.Fatalf("expected speed 150ms, got %v", g.Speed)
	}
}

func TestChangeDirectionNoReverse(t *testing.T) {
	g := NewGame(10, 10)
	g.Direction = DirUp
	g.NextDir = DirUp
	g.ChangeDirection(DirDown) // should be ignored
	if g.NextDir != DirUp {
		t.Fatal("should not allow reverse direction")
	}
	g.ChangeDirection(DirLeft) // should be allowed
	if g.NextDir != DirLeft {
		t.Fatal("should allow left when facing up")
	}
}

func TestTickWallCollision(t *testing.T) {
	g := NewGame(10, 10)
	// Place snake at left edge facing left, then tick
	g.Snake = []Point{{X: 0, Y: 5}, {X: 1, Y: 5}, {X: 2, Y: 5}}
	g.Direction = DirLeft
	g.NextDir = DirLeft
	g.Tick()
	if !g.GameOver {
		t.Fatal("expected game over after wall collision")
	}
}

func TestTickSelfCollision(t *testing.T) {
	g := NewGame(10, 10)
	// Create a snake that will run into itself
	g.Snake = []Point{
		{X: 5, Y: 5}, // head
		{X: 6, Y: 5},
		{X: 7, Y: 5},
		{X: 7, Y: 4},
		{X: 6, Y: 4},
		{X: 5, Y: 4},
		{X: 4, Y: 4},
	}
	g.Direction = DirRight
	g.NextDir = DirRight
	// Move forward 1 step -> head at (6,5), which is the snake body
	g.Tick()
	if !g.GameOver {
		t.Fatal("expected game over after self collision")
	}
}

func TestEatFood(t *testing.T) {
	g := NewGame(10, 10)
	g.Snake = []Point{{X: 5, Y: 5}, {X: 4, Y: 5}, {X: 3, Y: 5}}
	g.Direction = DirRight
	g.NextDir = DirRight
	g.Food = Point{X: 6, Y: 5} // place food right in front
	initialLen := len(g.Snake)
	g.Tick()
	if g.GameOver {
		t.Fatal("game should not be over after eating food")
	}
	if len(g.Snake) != initialLen+1 {
		t.Fatalf("snake should grow by 1 after eating, got len %d", len(g.Snake))
	}
	if g.Score != 1 {
		t.Fatalf("expected score 1, got %d", g.Score)
	}
}

func TestSpeedIncreaseAfter5Food(t *testing.T) {
	g := NewGame(10, 10)
	initialSpeed := g.Speed
	// Manually set score to 4, then eat one more
	g.Score = 4
	g.Snake = []Point{{X: 5, Y: 5}, {X: 4, Y: 5}, {X: 3, Y: 5}}
	g.Direction = DirRight
	g.NextDir = DirRight
	g.Food = Point{X: 6, Y: 5}
	g.Tick()
	if g.Score != 5 {
		t.Fatalf("expected score 5, got %d", g.Score)
	}
	if g.Speed >= initialSpeed {
		t.Fatal("speed should increase (decrease duration) after eating 5 food items")
	}
}

func TestIsWall(t *testing.T) {
	g := NewGame(10, 10)
	if !g.IsWall(-1, 5) {
		t.Fatal("(-1,5) should be a wall")
	}
	if !g.IsWall(10, 5) {
		t.Fatal("(10,5) should be a wall")
	}
	if !g.IsWall(5, -1) {
		t.Fatal("(5,-1) should be a wall")
	}
	if !g.IsWall(5, 10) {
		t.Fatal("(5,10) should be a wall")
	}
	if g.IsWall(0, 0) {
		t.Fatal("(0,0) should not be a wall")
	}
	if g.IsWall(9, 9) {
		t.Fatal("(9,9) should not be a wall")
	}
}

func TestStartReset(t *testing.T) {
	g := NewGame(10, 10)
	g.Score = 100
	g.GameOver = true
	g.Start()
	if g.GameOver {
		t.Fatal("game should not be over after reset")
	}
	if g.Score != 0 {
		t.Fatal("score should reset to 0")
	}
	if len(g.Snake) != 3 {
		t.Fatal("snake length should reset to 3")
	}
}
