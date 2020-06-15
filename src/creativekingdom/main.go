// TODO: you need tls for websockets to work somewhat reliably in practice
// Also fall back to http, longpolling etc if there are network issues!
// TODO: put timeout for any touch!
// TODO: remove player when conn leaves!
package main

import "net/http"
import "log"
import "time"
import "strings"
import "strconv"
import "math"
import "bytes"
import "fmt"
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
    Emoji string
    Conn *websocket.Conn
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
var gravityAccelerationMS2 float64 = 0.008

var moveRatePerMs = 0.6
func processGame() bool {
	dirty := false

	newTime := time.Now()
	elapsedMS := math.Floor(float64(newTime.Sub(lastTime).Nanoseconds()) / 1000000)
    //log.Printf("elapsed: %0.2f", elapsedMS)
	lastTime = newTime

    for _, p := range gameState.Players {

        // left
        if (p.ControlState & 1) == 1 {
            p.X -= (elapsedMS * moveRatePerMs)
            dirty = true
        }

        // right
        if (p.ControlState & 2) == 2 {
            p.X += (elapsedMS * moveRatePerMs)
            dirty = true
        }

        // up
        if (p.ControlState & 4) == 4 {
            p.Y -= (elapsedMS * moveRatePerMs)
            dirty = true
        }

        // down
        if (p.ControlState & 8) == 8 {
            p.Y += (elapsedMS * moveRatePerMs)
            dirty = true
        }


        // jump
        if (p.ControlState & 16) == 16 {

            if !p.IsJumping {
                p.IsJumping = true 

                // this next 2 allows us to jump again
                p.VelocityY = 0
                p.JumpAccelerationY = 0

                p.JumpStartTime = newTime
                p.JumpAccelerationY = (-0.016 * 1.2)
            } else if newTime.Sub(p.JumpStartTime) >= (100 * time.Millisecond) {
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

        var tmpFloor float64 = 1000
        if p.Y < tmpFloor {
            // TODO: only if something stable isn't under 
            p.VelocityY += gravityAccelerationMS2 * elapsedMS
            dirty = true
        }

        totalVelocity := p.VelocityY
        if totalVelocity != 0 {
            p.Y += totalVelocity * elapsedMS 
            if p.Y > tmpFloor {
                p.Y = tmpFloor 
                p.VelocityY = 0
            }
            dirty = true
        }
    }

    return dirty
}
func handlePlayerInput(playerInput PlayerInput) bool {
    p := string(playerInput.Payload) 
    parts := strings.Split(p, "|")
    if len(parts) < 2 {
        return false
    }
    playerID := parts[0]
    controlStateString := parts[1]

    emoji := ""
    if len(parts) >= 3 {
        emoji = parts[2] 
    }
    controlState, err := strconv.Atoi(controlStateString)
    if err != nil {
        return false
    }
    
    player, ok := playersById[playerID]
    if !ok {
        log.Printf("Yay new player and the emoji is %s", emoji)
        player = &Player{
            ID: parts[0],
            Color: "black",
            ControlState: controlState,
            X: 20,
            Y: 5,
            Emoji: emoji,
            Conn: playerInput.Conn,
        } 
        gameState.Players = append(gameState.Players, player)
        playersById[player.ID] = player
    } else {
        player.ControlState = controlState
        if emoji != "" {
            player.Emoji = emoji 
        }
    }

    return true
}



func main() {
	ticker := time.NewTicker(30 * time.Millisecond)
	//ticker := time.NewTicker(30 * time.Millisecond)
	//ticker := time.NewTicker(60 * time.Millisecond)
	lastTime := time.Now()	
	startTime := lastTime
    _ = startTime
    inputCh := make(chan PlayerInput, 1000)
    updateClientsCh := make(chan []byte, 1000)
    closeClientCh := make(chan *websocket.Conn, 1000)
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
                //gameStateJSON, err := json.Marshal(gameState) 
                var buf bytes.Buffer
                for _, p := range gameState.Players {
                    buf.Write([]byte(fmt.Sprintf("%0.0f|%0.0f|%s\n", p.X, p.Y, p.Emoji )))     
                }
                gameStateBytes := buf.Bytes()
                log.Printf("%s\n=============================\n\n", string(gameStateBytes))
                updateClientsCh <- gameStateBytes
            }
		}
	}()

    // TODO: potentially one goroutine per client?!
    go func() {
        for {
            select {
                case gameStateJSON := <- updateClientsCh:
                    for _, p := range gameState.Players {
                        //err := c.WriteMessage(0, gameStateJSON) 
                        err := p.Conn.WriteMessage(1, gameStateJSON) 
                        if err != nil {
                            log.Printf("client write error: %v", err) 
                            // TODO: you can close inline here and it will be more efficient
                            closeClientCh <- p.Conn
                        }
                    }    
                case conn := <- closeClientCh:
                    log.Printf("someone disconnected====")
                    foundIndex := -1
                    var foundPlayer *Player
                    for i, p := range gameState.Players {
                        if p.Conn == conn {
                            log.Printf("disconnected index is %d====", i)
                            foundIndex = i
                            foundPlayer = p
                            break 
                        }
                    }
                    if foundIndex != -1 {
                        // TODO look at possible memory leak  
                        // https://github.com/golang/go/wiki/SliceTricks
                        gameState.Players[foundIndex] = gameState.Players[len(gameState.Players)-1] 
                        gameState.Players = gameState.Players[:len(gameState.Players)-1]
                        delete(playersById, foundPlayer.ID)
                    }
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
		for {
            messageType, p, err := conn.ReadMessage()
            log.Printf("message type: %v, message: %s", messageType, string(p))
            if err != nil {
                log.Printf("error reading message: %v", err)
                return 
            }
            inputCh <- PlayerInput{Payload: p, Conn: conn}
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

type PlayerInput struct {
    Payload []byte    
    Conn *websocket.Conn
}
