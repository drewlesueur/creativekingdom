// TODO: you need tls for websockets to work somewhat reliably in practice
// Also fall back to http, longpolling etc if there are network issues!
package main

import "net/http"
import "log"
import "time"
import "strings"
import "strconv"
import "encoding/json"
import "math"
import "github.com/gorilla/websocket"

type Player struct {	
    VelocityY float64
    VelocityX float64

    IsJumping bool
    JumpStartTime time.Time
    JumpAccelerationY float64
    // TODO: array of accelerations?!

	ID string
	// Bitmask: Jump Left Right
	ControlState int
	Color string
    X float64
    Y float64
}

type GameState struct {
    Players []*Player
}

var lastTime time.Time
var startTime time.Time

var playersById map[string]*Player
var conns []*websocket.Conn
var gameState *GameState

// objects too?

// per second per second
var gravityAccelerationMS2 float64 = 0.004

var moveRatePerMs = 0.2
func processGame() bool {
	dirty := false

	newTime := time.Now()
	elapsedMS := math.Floor(float64(newTime.Sub(lastTime).Nanoseconds()) / 1000000)
    //log.Printf("elapsed: %0.2f", elapsedMS)
	lastTime = newTime

    for _, p := range gameState.Players {
        if (p.ControlState & 1) == 1 {
            p.X += (elapsedMS * moveRatePerMs)
            dirty = true
        }

        if (p.ControlState & 2) == 2 {
            p.X -= (elapsedMS * moveRatePerMs)
            dirty = true
        }

        if (p.ControlState & 4) == 4 {
            p.Y -= (elapsedMS * moveRatePerMs)
            dirty = true
        }

        if (p.ControlState & 8) == 8 {
            p.Y += (elapsedMS * moveRatePerMs)
            dirty = true
        }

        if (p.ControlState & 16) == 16 {
            if !p.IsJumping {
                p.IsJumping = true 
                p.JumpStartTime = newTime
                p.JumpAccelerationY = -0.006
            } else if newTime.Sub(p.JumpStartTime) >= (200 * time.Millisecond) {
                p.JumpAccelerationY = 0
                 
            }
            p.VelocityY += p.JumpAccelerationY * elapsedMS

            //log.Printf("jumping velocity: %v", p.VelocityY)
            dirty = true
        } else {
            if (p.IsJumping) {
                p.JumpAccelerationY = 0 
                p.IsJumping = false
                dirty = true
            }
        }


        if p.Y < 100 {
            // TODO: only if something stable isn't under 
            p.VelocityY += gravityAccelerationMS2 * elapsedMS
            dirty = true
        }

        if p.VelocityY != 0 {
            p.Y += p.VelocityY * elapsedMS 
            if p.Y > 100 {
                p.Y = 100 
                p.VelocityY = 0
            }
            dirty = true
        }
    }

    return dirty
}
func handlePlayerInput(playerInput []byte) bool {
    p := string(playerInput) 
    parts := strings.Split(p, "|")
    if len(parts) < 2 {
        return false
    }
    playerID := parts[0]
    controlStateString := parts[1]
    controlState, err := strconv.Atoi(controlStateString)
    if err != nil {
        return false
    }
    
    player, ok := playersById[playerID]
    if !ok {
        player = &Player{
            ID: parts[0],
            Color: "black",
            ControlState: controlState,
            X: 5,
            Y: 5,
        } 
        gameState.Players = append(gameState.Players, player)
        playersById[player.ID] = player
    } else {
        player.ControlState = controlState
    }


    return true
}



func main() {
	lastTime := time.Now()	
	startTime := lastTime
    _ = startTime
    inputCh := make(chan []byte, 1000)
    updateClientsCh := make(chan []byte, 1000)
    newClientsCh := make(chan *websocket.Conn, 1000)
	ticker := time.NewTicker(20 * time.Millisecond)
	//ticker := time.NewTicker(500 * time.Millisecond)
    playersById = map[string]*Player{}
    gameState = &GameState{Players: []*Player{}}

	go func() {
        // All game state gets updated in this gofunc and no other
		for {
            dirty := false
            select {
                case <- ticker.C:
                    dirty = processGame()
                case playerInput := <-inputCh:
                    dirty = handlePlayerInput(playerInput)
            }
            //log.Printf("game loop: dirty: %t, num players: %d", dirty, len(gameState.Players))
            if dirty {
                gameStateJSON, err := json.Marshal(gameState) 
                log.Printf("game json: %v", string(gameStateJSON))
                if err != nil {
                    log.Printf("error encoding game state: %v", err) 
                    continue
                }
                updateClientsCh <- gameStateJSON
            }
		}
	}()

    // TODO: potentially one goroutine per client?!
    go func() {
        for {
            select {
                case gameStateJSON := <- updateClientsCh:
                    for _, c := range conns {
                        //err := c.WriteMessage(0, gameStateJSON) 
                        err := c.WriteMessage(1, gameStateJSON) 
                        if err != nil {
                            // TODO: figure out how to remove the player and the conn!
                            log.Printf("client write error: %v", err) 
                        }
                    }    
                case conn := <- newClientsCh:
                    conns = append(conns, conn)
                // TODO: delete a conn
            }
        } 
    }()

	var upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}

    mux := http.NewServeMux()
    mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        http.ServeFile(w, r, "index.html") 
    })

    mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("error upgrading: %v", err)
			return
		}
        newClientsCh <- conn
		for {
            messageType, p, err := conn.ReadMessage()
            log.Printf("message type: %v", messageType)
            if err != nil {
                log.Printf("error reading message: %v", err)
                return 
            }
            inputCh <- p
		}
    })

    addr := ":8036"
    srv := &http.Server{
        Addr: addr,
        Handler: mux, 
    }
    log.Printf("Listening on " + addr)
    log.Fatal(srv.ListenAndServe())
}
