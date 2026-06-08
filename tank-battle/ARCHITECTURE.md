# 坦克大战 (Tank Battle) — Game Architecture Document

> **Version:** 1.0  
> **Target Framework:** Python 3.11+ / Pygame 2.6+  
> **Inspired By:** *Battle City* (1985, Namco)

---

## 1. Overview

This document defines the complete architecture for a classic "Battle City"-style
tank battle game. The game is a top‑down, tile‑based, two‑player (one human vs.
CPU) shooter where the player defends a **base** (an eagle flag) from waves of
enemy tanks.

---

## 2. Module Organization

```
tank-battle/
├── main.py                  # Entry point: bootstraps everything
├── config.py                # Constants, tuning knobs, asset paths
├── game/
│   ├── __init__.py
│   ├── game.py              # Game class — owns the main loop
│   ├── state_machine.py     # Menu, Playing, GameOver, LevelComplete states
│   ├── level.py             # Level loading, tile-map parsing, wave definitions
│   └── camera.py            # (Optional) viewport if maps exceed screen size
├── entities/
│   ├── __init__.py
│   ├── tank.py              # Base Tank class
│   ├── player_tank.py       # PlayerTank(PlayerTank)
│   ├── enemy_tank.py        # EnemyTank(AITank) with different difficulty tiers
│   ├── bullet.py            # Bullet entity
│   ├── wall.py              # Wall (brick, steel, forest, water, ice)
│   └── base.py              # The eagle flag / home base
├── components/
│   ├── __init__.py
│   ├── input.py             # Keyboard/joystick input handling
│   ├── physics.py           # Movement, collision, AABB helpers
│   ├── ai.py                # Enemy AI: state machine (patrol, chase, attack)
│   ├── spawner.py           # Enemy spawn logic with timed waves
│   ├── particles.py         # Explosion effects, bullet trails
│   └── sound.py             # SFX playback manager
├── systems/
│   ├── __init__.py
│   ├── collision_system.py  # Centralised collision detection & resolution
│   └── render_system.py     # Layered rendering (z‑order)
├── ui/
│   ├── __init__.py
│   ├── hud.py               # Score, lives, level indicator
│   ├── menu.py              # Title screen, pause overlay
│   └── font.py              # Pixel font renderer / bitmap font loader
├── resources/
│   ├── sprites/             # PNG / GIF tileset images
│   ├── sounds/              # WAV / OGG sound effects
│   ├── maps/                # Tiled (.tmx / .csv) or custom plain-text maps
│   └── config/              # Game balance JSON files
└── ARCHITECTURE.md          # (this file)
```

---

## 3. Class Hierarchy

```
┌────────────────────────────────────────────────────────┐
│                     Entity (ABC)                        │
│  + rect: pygame.Rect                                   │
│  + image: pygame.Surface                               │
│  + active: bool                                        │
│  + update(dt) → None  (abstract)                       │
│  + draw(surface) → None                                │
└───────────────────────────┬────────────────────────────┘
                            │
          ┌─────────────────┼─────────────────┬─────────────────┐
          │                 │                 │                 │
    ┌─────┴─────┐    ┌─────┴─────┐    ┌──────┴──────┐   ┌────┴────┐
    │   Tank    │    │  Bullet   │    │    Wall     │   │  Base   │
    │ (ABC)     │    │           │    │             │   │         │
    │ + speed   │    │ + dir     │    │ + hp        │   │ + hp    │
    │ + hp      │    │ + damage  │    │ + wall_type │   │ + alive │
    │ + dir     │    │ + owner   │    │             │   │         │
    │ + cooldown│    │ + move()  │    └─────────────┘   └─────────┘
    │ + fire()  │    └───────────┘
    └─────┬─────┘
          │
    ┌─────┴─────────────────┐
    │                       │
┌───┴───────┐         ┌─────┴──────┐
│PlayerTank │         │ EnemyTank  │
│           │         │            │
│ + lives   │         │ + tier     │
│ + score   │         │ + ai_state │
│ + shield  │         │ + update_ai│
│ + input() │         │            │
└───────────┘         └────────────┘
```

### 3.1 Entity (Abstract Base)

```python
class Entity(ABC):
    def __init__(self, x: int, y: int):
        self.rect = pygame.Rect(x, y, TILE_SIZE, TILE_SIZE)
        self.image: pygame.Surface | None = None
        self.active = True

    @abstractmethod
    def update(self, dt: float) -> None:
        """Called every frame. Subclasses implement logic here."""
        pass

    def draw(self, surface: pygame.Surface) -> None:
        if self.image and self.active:
            surface.blit(self.image, self.rect.topleft)
```

### 3.2 Tank (Abstract)

```python
class Tank(Entity):
    DIR_UP, DIR_RIGHT, DIR_DOWN, DIR_LEFT = range(4)

    def __init__(self, x, y, hp=1, speed=2.0):
        super().__init__(x, y)
        self.hp = hp
        self.max_hp = hp
        self.speed = speed
        self.direction = Tank.DIR_UP
        self.facing = None           # last movement direction
        self.cooldown_timer = 0.0    # seconds until next shot allowed
        self.fire_rate = 0.5         # shots / second
        self.bullet_speed = 4.0

    def move(self, dx, dy, dt, obstacles) -> bool:
        """Move by (dx*dt, dy*dt), return True if moved."""
        pass

    def fire(self) -> Bullet | None:
        """Return a Bullet if cooldown allows, else None."""
        if self.cooldown_timer <= 0:
            self.cooldown_timer = 1.0 / self.fire_rate
            return Bullet(self, self.direction)
        return None
```

### 3.3 PlayerTank

```python
class PlayerTank(Tank):
    def __init__(self, x, y, player_id=1):
        super().__init__(x, y, hp=1, speed=2.5)
        self.player_id = player_id
        self.lives = 3
        self.shield_timer = 3.0        # spawn invincibility (seconds)
        self.score = 0
        self.powerup_flags: set = set()  # {"speed", "shield", "freeze", "shovel"}

    def handle_input(self, keys, events) -> Bullet | None:
        """Read keyboard state, update direction/velocity, return Bullet if fired."""
        ...

    def apply_powerup(self, powerup: str) -> None:
        """Activate a collected power-up."""
        ...
```

### 3.4 EnemyTank

```python
class EnemyTank(Tank):
    TIER_BASIC = {"hp": 1, "speed": 1.0, "fire_rate": 1.0, "score": 100}
    TIER_FAST  = {"hp": 1, "speed": 2.5, "fire_rate": 2.0, "score": 200}
    TIER_HEAVY = {"hp": 4, "speed": 0.8, "fire_rate": 0.5, "score": 300}
    TIERS = [TIER_BASIC, TIER_FAST, TIER_HEAVY]

    def __init__(self, x, y, tier=0):
        super().__init__(x, y)
        self.tier = tier
        cfg = self.TIERS[tier]
        self.hp = cfg["hp"]
        self.speed = cfg["speed"]
        self.fire_rate = cfg["fire_rate"]
        self.score_value = cfg["score"]

        # AI state machine
        self.ai_state = "PATROL"   # PATROL → CHASE → ATTACK
        self.ai_timer = 0.0
        self.direction_change_interval = 2.0  # seconds

    def update_ai(self, player_pos, base_pos, dt) -> None:
        """Decide next action based on game state."""
        ...
```

### 3.5 Bullet

```python
class Bullet(Entity):
    def __init__(self, owner: Tank, direction: int):
        # spawn just in front of the tank
        spawn_offset = {
            Tank.DIR_UP:    (owner.rect.centerx - BULLET_SIZE//2, owner.rect.top - BULLET_SIZE),
            Tank.DIR_DOWN:  (owner.rect.centerx - BULLET_SIZE//2, owner.rect.bottom),
            Tank.DIR_LEFT:  (owner.rect.left - BULLET_SIZE, owner.rect.centery - BULLET_SIZE//2),
            Tank.DIR_RIGHT: (owner.rect.right, owner.rect.centery - BULLET_SIZE//2),
        }
        super().__init__(*spawn_offset[direction])
        self.owner = owner
        self.direction = direction
        self.speed = owner.bullet_speed
        self.damage = 1
        self.active = True

    def update(self, dt: float) -> None:
        """Move in direction. Mark inactive when out of bounds."""
        vectors = {
            Tank.DIR_UP:    (0, -self.speed * dt),
            Tank.DIR_DOWN:  (0,  self.speed * dt),
            Tank.DIR_LEFT:  (-self.speed * dt, 0),
            Tank.DIR_RIGHT: ( self.speed * dt, 0),
        }
        dx, dy = vectors[self.direction]
        self.rect.x += int(dx)
        self.rect.y += int(dy)
        # Out-of-bounds check
        if not SCREEN_RECT.contains(self.rect):
            self.active = False
```

### 3.6 Wall

```python
class Wall(Entity):
    BRICK, STEEL, FOREST, WATER, ICE = range(5)

    # Collision / interaction matrix
    WALL_PROPERTIES = {
        BRICK: {"hp": 1,  "bullet_pass": False, "tank_pass": False, "destroyable": True},
        STEEL: {"hp": 99, "bullet_pass": False, "tank_pass": False, "destroyable": True},  # only by special bullets
        FOREST: {"hp": 1, "bullet_pass": True,  "tank_pass": True,  "destroyable": False},  # visual cover only
        WATER:  {"hp": 1, "bullet_pass": False, "tank_pass": False, "destroyable": False},  # impassable
        ICE:    {"hp": 1, "bullet_pass": True,  "tank_pass": True,  "destroyable": False, "slippery": True},
    }

    def __init__(self, x, y, wall_type: int):
        super().__init__(x, y)
        self.wall_type = wall_type
        props = self.WALL_PROPERTIES[wall_type]
        self.hp = props["hp"]
        self.bullet_pass = props["bullet_pass"]
        self.tank_pass = props["tank_pass"]
        self.destroyable = props["destroyable"]
        self.slippery = props.get("slippery", False)

    def take_damage(self, amount=1) -> bool:
        """Return True if destroyed."""
        self.hp -= amount
        if self.hp <= 0 and self.destroyable:
            self.active = False
            return True
        return False
```

### 3.7 Base

```python
class Base(Entity):
    def __init__(self, x, y):
        super().__init__(x, y)
        self.hp = 1
        self.alive = True

    def take_damage(self) -> bool:
        self.alive = False
        self.active = False
        return True   # game over condition
```

---

## 4. Core Systems

### 4.1 Game Loop (`game.py`)

```
while running:
    dt = clock.tick(FPS) / 1000.0           # delta time in seconds

    # 1. Handle Events
    for event in pygame.event.get():
        if event.type == QUIT: running = False
        state_machine.handle_event(event)

    # 2. Update State Machine (menu → playing → game_over → ...)
    state_machine.update(dt)

    # 3. If state is PLAYING:
    #    a. Read input → produce Bullets
    #    b. Update Enemy AI
    #    c. Update all entities (Tanks, Bullets, Particles)
    #    d. Collision detection & resolution
    #    e. Spawner tick (time-based)
    #    f. Check win/lose conditions

    # 4. Render
    #    a. Clear screen
    #    b. Draw tile map (terrain layer)
    #    c. Draw Walls
    #    d. Draw Tanks (sorted by y for pseudo-depth)
    #    e. Draw Bullets
    #    f. Draw Base
    #    g. Draw particles / effects
    #    h. Draw HUD overlay
    #    i. Flip display

    pygame.display.flip()
```

### 4.2 State Machine (`state_machine.py`)

```
           ┌──────────┐
           │  MENU    │
           └────┬─────┘
                │ SPACE/ENTER
           ┌────▼─────┐
           │ PLAYING  │◄────────────┐
           └────┬─────┘             │
                │                   │
          ┌─────┴──────┐           │
          │            │           │
    ┌─────▼────┐ ┌─────▼────┐     │
    │  GAME    │ │  LEVEL   │─────┘
    │  OVER    │ │ COMPLETE │  (next level)
    └──────────┘ └──────────┘
```

States are implemented via the **State Pattern**:

```python
class State(ABC):
    @abstractmethod
    def enter(self, game: "Game") -> None: ...
    @abstractmethod
    def exit(self, game: "Game") -> None: ...
    @abstractmethod
    def handle_event(self, event) -> None: ...
    @abstractmethod
    def update(self, dt: float) -> None: ...
    @abstractmethod
    def draw(self, surface) -> None: ...

class StateMachine:
    def __init__(self):
        self.states: dict[str, State] = {}
        self.current: str | None = None

    def add(self, name: str, state: State) -> None: ...
    def change(self, name: str, game) -> None: ...
    def handle_event(self, event) -> None: ...
    def update(self, dt: float) -> None: ...
    def draw(self, surface) -> None: ...
```

### 4.3 Collision Detection (`collision_system.py`)

**Approach:** Discrete AABB (Axis-Aligned Bounding Box) with spatial partitioning.

```python
class CollisionSystem:
    """Central authority for all collision checks each frame."""

    GRID_SIZE = TILE_SIZE * 4   # coarse spatial cell (4×4 tiles)

    def __init__(self, map_width, map_height):
        self.grid: dict[tuple, list[Entity]] = defaultdict(list)

    def register(self, entity: Entity) -> None:
        """Insert entity into spatial grid cells it overlaps."""
        pass

    def rebuild(self, entities: list[Entity]) -> None:
        """Clear and re-insert all active entities."""
        self.grid.clear()
        for e in entities:
            if e.active:
                self._insert(e)

    def query(self, entity: Entity) -> list[Entity]:
        """Return potential colliders from the same grid cells + neighbours."""
        pass

    def resolve_all(self, dt: float, tanks, bullets, walls, base) -> None:
        """
        1. Tank ↔ Wall          → push tank out, stop movement
        2. Tank ↔ Tank          → push apart, no overlap
        3. Tank ↔ Base          → game over
        4. Bullet ↔ Wall        → destroy wall (if BRICK) or remove bullet
        5. Bullet ↔ Tank        → damage tank, remove bullet
           (skip if bullet.owner is the same tank)
        6. Bullet ↔ Bullet      → both removed (mutual destruction)
        7. Bullet ↔ Base        → game over
        """
        ...
```

**Collision Resolution Order (per frame):**

| Step | Pair | Resolution |
|------|------|------------|
| 1 | Tank → Walls | Separate tank to nearest valid grid position |
| 2 | Tank → Tanks | Separate both tanks equally |
| 3 | Tank → Base | If overlapping, player loses (game over) |
| 4 | Bullet → Walls | Remove bullet; decrement wall HP |
| 5 | Bullet → Tanks | Remove bullet; decrement tank HP |
| 6 | Bullet → Bullets | Remove both bullets |
| 7 | Bullet → Base | Game over condition flag |

### 4.4 Enemy AI (`ai.py`)

Simple finite-state machine per enemy tank:

```python
class EnemyAI:
    def __init__(self, tank: EnemyTank):
        self.tank = tank
        self.state = "PATROL"
        self.target: tuple[int, int] | None = None
        self.patrol_path: list[tuple[int, int]] = []

    def update(self, player_pos, base_pos, walls, dt):
        if self.state == "PATROL":
            # Move toward a random direction
            # Change direction every ~2 seconds
            # If line-of-sight to player or base → switch to CHASE
            pass
        elif self.state == "CHASE":
            # Pathfind toward nearest target (player or base)
            # Shoot when aligned horizontally/vertically
            # If within 3 tiles of target → switch to ATTACK
            pass
        elif self.state == "ATTACK":
            # Fire repeatedly; strafe slightly
            pass
```

**Simplified Pathfinding:** No full A*. Use a **greedy movement** heuristic:
1. Try to align either X or Y with target.
2. If blocked by a wall, try the orthogonal direction (randomly choose left/right or up/down).
3. If still blocked, reverse direction.

This is sufficient because the grid is small (26×26 tiles) and enemies are numerous — A* per frame would be overkill.

### 4.5 Spawner (`spawner.py`)

```python
class Spawner:
    SPAWN_POINTS = [(0, 0), (12*TILE_SIZE, 0), (24*TILE_SIZE, 0)]
    WAVE_CONFIG = {
        1: {"total": 6,  "tiers": [0,0,0,0,1,1],   "spawn_interval": 4.0},
        2: {"total": 8,  "tiers": [0,0,1,1,1,2,2],   "spawn_interval": 3.5},
        3: {"total": 10, "tiers": [0,1,1,2,2,2,2],   "spawn_interval": 3.0},
        # ... config per level
    }

    def __init__(self):
        self.spawn_queue: list[int] = []   # tier indices
        self.timer = 0.0
        self.active_spawned = 0
        self.max_concurrent = 4

    def load_wave(self, level: int) -> None: ...

    def update(self, dt, current_enemies: list[EnemyTank], game) -> Bullet | None:
        """Spawn a new enemy when timer elapses AND concurrent < max."""
        self.timer -= dt
        if self.timer <= 0 and len(current_enemies) < self.max_concurrent:
            if self.spawn_queue:
                tier = self.spawn_queue.pop(0)
                point = random.choice(self.SPAWN_POINTS)
                enemy = EnemyTank(*point, tier)
                self.active_spawned += 1
                self.timer = self.WAVE_CONFIG[self.level]["spawn_interval"]
                return enemy
        return None
```

---

## 5. Map & Level Data

### 5.1 Tile Map Format

Maps are stored as plain-text grids (26 rows × 26 columns) in `resources/maps/`:

```
# Level 1 (level1.txt)
# 0 = empty, 1 = brick, 2 = steel, 3 = forest, 4 = water, 5 = ice
# 6 = base position (bottom center)
# 7 = player start (bottom left)
# 8, 9 = enemy spawn points (top)

00000000000000000000000000
00001111100100111110000000
00001000100000010001000000
00001000101111100101000000
...
```

### 5.2 Level Loader (`level.py`)

```python
class Level:
    WIDTH = 26   # tiles
    HEIGHT = 26  # tiles

    def __init__(self, id: int):
        self.id = id
        self.map_data: list[list[int]] = []
        self.walls: list[Wall] = []
        self.player_spawn: tuple[int, int] = (8*TILE_SIZE, 24*TILE_SIZE)
        self.enemy_spawns: list[tuple[int, int]] = [(0,0), (12*TILE_SIZE, 0), (24*TILE_SIZE, 0)]
        self.base_pos: tuple[int, int] = (12*TILE_SIZE, 24*TILE_SIZE)

    def load(self, path: str) -> None:
        """Parse map file, instantiate Wall objects."""
        ...

    def get_wave_config(self) -> dict:
        """Return spawner config for this level."""
        return Spawner.WAVE_CONFIG.get(self.id, Spawner.WAVE_CONFIG[1])
```

---

## 6. Data Flow (Per Frame)

```
                        ┌──────────────────────┐
                        │     pygame.event.get  │
                        └──────────┬───────────┘
                                   │
                                   ▼
                        ┌──────────────────────┐
                        │   Input Handler      │
                        │   (keyboard/joystick)│
                        └──────────┬───────────┘
                                   │
                ┌──────────────────┼──────────────────┐
                │                  │                  │
                ▼                  ▼                  ▼
        ┌────────────┐    ┌──────────────┐   ┌──────────────┐
        │PlayerTank  │    │  EnemyTank[] │   │   Spawner    │
        │.handle_    │    │  .update_ai  │   │   .update()  │
        │ input()    │    │  (per enemy) │   │   → new enemy│
        └─────┬──────┘    └──────┬───────┘   └──────┬───────┘
              │                  │                   │
              │    ┌─────────────▼─────────────┐     │
              └────►     CollisionSystem       ◄─────┘
                   │     .resolve_all()        │
                   └─────────────┬─────────────┘
                                 │
                                 ▼
                   ┌─────────────────────────┐
                   │   Check Game Conditions │
                   │   • Base destroyed?     │
                   │   • Player dead?        │
                   │   • All enemies killed? │
                   └─────────────┬───────────┘
                                 │
                                 ▼
                   ┌─────────────────────────┐
                   │   RenderSystem          │
                   │   .draw_all(entities)   │
                   └─────────────┬───────────┘
                                 │
                                 ▼
                   ┌─────────────────────────┐
                   │   HUD overlay           │
                   │   Score, Lives, Level   │
                   └─────────────────────────┘
```

---

## 7. Scoring & Progression

### 7.1 Score Table

| Event | Points |
|-------|--------|
| Destroy Basic Enemy | 100 |
| Destroy Fast Enemy | 200 |
| Destroy Heavy Enemy | 300 |
| Collect Power-up | 500 |
| Level Completion Bonus | 1000 × lives_remaining |

### 7.2 Life & Progression Rules

- Player starts with **3 lives**.
- When hit by a bullet: lose 1 life, respawn at player_start after 2 seconds (with 3-second shield).
- If lives == 0: **Game Over**.
- When all wave enemies are cleared + no active enemies: **Level Complete**.
- After level complete → increment level index → load next map → reset spawner.
- After the last level (typically 35 in classic) → **Victory** screen.

### 7.3 Power-up Spawning

After destroying a certain number of enemy tanks (configurable, e.g., every 2nd or 3rd enemy), a **power-up crate** spawns randomly on the map. Power-ups:

| Power-up | Effect | Duration |
|----------|--------|----------|
| **Shield** | Invincibility around the tank | 10 seconds |
| **Speed** | 50% movement speed boost | 10 seconds |
| **Shovel** | Builds steel wall around base | Permanent (until destroyed) |
| **Freeze** | Freezes all enemies | 10 seconds |
| **Bomb** | Destroys all enemies on screen | Instant |

---

## 8. Configuration (`config.py`)

```python
# ── Display ──
SCREEN_WIDTH = 832               # 26 tiles × 32 px
SCREEN_HEIGHT = 832
TILE_SIZE = 32
FPS = 60
BULLET_SIZE = 8

# ── Tuning ──
PLAYER_SPEED = 2.5               # tiles per second
PLAYER_FIRE_RATE = 2.0           # shots per second
PLAYER_BULLET_SPEED = 4.0        # tiles per second
PLAYER_LIVES = 3
PLAYER_SHIELD_DURATION = 3.0     # seconds

ENEMY_SPEEDS = [1.0, 2.5, 0.8]
ENEMY_FIRE_RATES = [1.0, 2.0, 0.5]
ENEMY_BULLET_SPEED = 3.0
MAX_CONCURRENT_ENEMIES = 4

SPAWN_INTERVAL = 3.0             # seconds between enemy spawns

# ── Paths ──
SPRITE_DIR = "resources/sprites"
SOUND_DIR = "resources/sounds"
MAP_DIR = "resources/maps"
```

---

## 9. Rendering Pipeline (`render_system.py`)

Entities are drawn in strict z-order:

```
Layer 0: Background / tile map (empty cells, water tiles)
Layer 1: Forest tiles (blocks vision — drawn as overlay above tanks)
Layer 2: Walls (brick, steel)
Layer 3: Tank shadows (optional)
Layer 4: Tanks
Layer 5: Bullets
Layer 6: Base (eagle flag)
Layer 7: Particles / explosions
Layer 8: Power-up crates
Layer 9: HUD / UI overlay (score, lives, pause menu)
```

**Camera note:** For the standard game, the map is exactly 26×26 tiles fitting the screen. If future levels support larger maps, implement a simple **viewport camera** that follows the player and clamps to map bounds.

---

## 10. Sprite & Asset Strategy

- Use a **single tile sheet image** (`sprites/tiles.png`) with all assets arranged in a grid.
- Load sprites at startup and cache them in a dictionary: `SPRITES[key] = surface`.
- Expected sprite keys:

```
player_1_up, player_1_down, player_1_left, player_1_right    # Player tank facing each direction
player_2_up, player_2_down, player_2_left, player_2_right    # Player 2 (co-op)
enemy_basic_up, enemy_basic_down, ...                         # Basic enemy
enemy_fast_up, enemy_fast_down, ...                           # Fast enemy
enemy_heavy_up, enemy_heavy_down, ...                         # Heavy enemy
brick, steel, forest, water, ice                              # Wall tiles
base_alive, base_destroyed                                    # Eagle flag sprites
bullet_player, bullet_enemy                                   # Bullets (different colours)
explosion_1, explosion_2, explosion_3                         # Explosion animation frames
powerup_shield, powerup_speed, powerup_shovel, powerup_freeze, powerup_bomb
```

For a quick start, sprites can be generated programmatically (coloured rectangles with details) via a `sprites_generator.py` utility.

---

## 11. Edge Cases & Risk Mitigation

| Risk | Mitigation |
|------|------------|
| **Bullet stuck inside wall** | Spawn bullets *outside* the tank rect and use continuous collision check (sub-step movement) |
| **Tank overlapping after collision** | Separate along the axis of least penetration. Apply position correction BEFORE rendering |
| **Two bullets cancel each other** | Store `bullets_to_remove` set; process at end of collision pass |
| **Enemy shooting through its own spawn point** | Add a brief "spawn invulnerability" timer for enemies |
| **Base destroyed by stray bullet** | Check base collision BEFORE bullet-wall so it's not missed; flag game over immediately |
| **Performance with many bullets** | Cap total bullets at 64; spatial grid reduces collision checks from O(n²) to O(n) |
| **Delta-time spikes (lag)** | Clamp `dt` to max 0.05 seconds (50ms) to prevent tunnelling |
| **Frozen enemies firing** | `Freeze` power-up also sets enemy `fire_rate` to 0 |
| **Player trapped by walls** | Ensure at least one valid path from spawn to map middle in each level |

---

## 12. Dependency Graph (Import Map)

```
main.py
 └─ config.py
 └─ game.game
      └─ game.state_machine
      │    └─ ui.menu
      │    └─ game.level
      │    └─ components.spawner
      │    └─ entities.player_tank
      │    └─ entities.enemy_tank
      │    └─ entities.bullet
      │    └─ entities.wall
      │    └─ entities.base
      │    └─ components.input
      │    └─ components.ai
      │    └─ components.particles
      │    └─ systems.collision_system
      │    └─ systems.render_system
      │    └─ ui.hud
      └─ components.sound
```

No circular dependencies. Systems depend on entity definitions never vice‑versa.

---

## 13. Implementation Order (Recommended Build Sequence)

**Phase 1 — Core (Day 1)**
1. `config.py` — constants
2. `entities/__init__.py` + `Entity` abstract base
3. `entities/wall.py` — simplest entity
4. `entities/base.py`
5. `entities/tank.py` + `entities/player_tank.py` — with basic movement (no collision)
6. `entities/bullet.py` — fire a bullet, watch it fly off-screen
7. `entities/enemy_tank.py` — dummy AI (just drives down)
8. `game/level.py` — load a hard-coded map; instantiate walls

**Phase 2 — Playability (Day 2)**
9. `components/input.py` — WASD + Space
10. `components/physics.py` — movement with wall collision resolution
11. `systems/collision_system.py` — full collision matrix
12. `components/spawner.py` — timed enemy spawning
13. `components/ai.py` — patrol + chase AI

**Phase 3 — Polish (Day 3)**
14. `game/state_machine.py` — MENU, PLAYING, GAME_OVER, LEVEL_COMPLETE
15. `ui/hud.py` — score, lives, level
16. `ui/menu.py` — start screen
17. `components/particles.py` — explosions
18. `components/sound.py` — SFX
19. `systems/render_system.py` — z-order rendering
20. `resources/sprites/` — tile sheet and sprite loading

**Phase 4 — Content (Day 4+)**
21. Additional level maps (level1.txt … levelN.txt)
22. Power-up system
23. Level progression and win condition
24. Two‑player co‑op support
25. High-score persistence (JSON file)

---

## 14. Summary

This architecture delivers a clean, modular, testable tank battle game. Key design decisions:

| Decision | Rationale |
|----------|-----------|
| **ECS‑inspired** but not full ECS | Keeps code approachable; composition via components where useful |
| **State machine** for game flow | Clean separation of menu/playing/paused/game_over |
| **Spatial grid** for collision | O(n) collision vs O(n²) — essential for 10+ enemies + bullets |
| **Greedy AI** instead of A* | Good enough on small grid; avoids pathfinding overhead per‑frame |
| **Tile‑based map** | Matches original game; simplifies wall placement, collision, level editing |
| **Discrete AABB collision** | Simplest correct approach; sub-step for fast bullets |
| **Plain‑text map files** | Easy to edit, parse, and generate procedurally |

---
