// // manager.go (fixed: proper offset handling, dedupe results, message throttling)
// package main

// import (
// 	"bytes"
// 	"encoding/json"
// 	"fmt"
// 	"io"
// 	"log"
// 	"net/http"
// 	"net/url"
// 	"os"
// 	"strconv"
// 	"strings"
// 	"sync"
// 	"time"
// )

// type Command struct {
// 	ID      string `json:"id"`
// 	Cmd     string `json:"cmd"`
// 	Created int64  `json:"created"`
// }

// type Device struct {
// 	ID        string     `json:"id"`
// 	Name      string     `json:"name"`
// 	LastSeen  int64      `json:"last_seen"`
// 	Queue     []Command  `json:"queue"`
// 	QueueLock sync.Mutex `json:"-"`
// }

// var (
// 	telegramToken = os.Getenv("TELEGRAM_BOT_TOKEN")
// 	allowedIDEnv  = os.Getenv("ALLOWED_USER_ID")
// 	passPhrase    = os.Getenv("PASS_PHRASE")
// 	sharedSecret  = os.Getenv("SHARED_SECRET") // optional
// 	managerPort   = os.Getenv("MANAGER_PORT")  // optional, default 8080

// 	apiBase string
// 	allowID int64

// 	devices   = map[string]*Device{}
// 	devicesMu sync.RWMutex

// 	// Для защиты от повторных результатов (cmd_id -> timestamp)
// 	recentResults   = map[string]int64{}
// 	recentResultsMu sync.Mutex

// 	// Для троттлинга одинаковых сообщений (text -> last sent time)
// 	lastSentMap   = map[string]int64{}
// 	lastSentMapMu sync.Mutex

// 	// конфигурация дедупа/троттла
// 	resultDuplicateWindowSec = int64(60) // игнорировать одинаковые cmd_id в течение 60 сек
// 	messageThrottleSec       = int64(3)  // не отправлять одинаковые текстовые сообщения чаще, чем раз в 3 сек
// )

// func main() {
// 	if telegramToken == "" || allowedIDEnv == "" || passPhrase == "" {
// 		log.Fatal("Set TELEGRAM_BOT_TOKEN, ALLOWED_USER_ID and PASS_PHRASE environment variables")
// 	}
// 	var err error
// 	allowID, err = strconv.ParseInt(allowedIDEnv, 10, 64)
// 	if err != nil {
// 		log.Fatalf("Bad ALLOWED_USER_ID: %v", err)
// 	}
// 	if managerPort == "" {
// 		managerPort = "8080"
// 	}
// 	apiBase = "https://api.telegram.org/bot" + telegramToken + "/"

// 	// HTTP endpoints
// 	http.HandleFunc("/register", handleRegister)
// 	http.HandleFunc("/poll", handlePoll)
// 	http.HandleFunc("/result", handleResult)
// 	http.HandleFunc("/devices", handleDevices) // for quick debugging

// 	serverAddr := ":" + managerPort
// 	go func() {
// 		log.Printf("Starting HTTP server at %s\n", serverAddr)
// 		if err := http.ListenAndServe(serverAddr, nil); err != nil {
// 			log.Fatalf("ListenAndServe: %v", err)
// 		}
// 	}()

// 	// Start telegram long-polling handler (fixed offset handling)
// 	go telegramLoop()

// 	// Send startup message (attempt once)
// 	sendMessage(allowID, fmt.Sprintf("Manager started at %s (port %s). Shared secret: %v", time.Now().Format(time.RFC3339), managerPort, sharedSecret != ""))

// 	// keep main alive
// 	select {}
// }

// // === HTTP Handlers ===

// func handleRegister(w http.ResponseWriter, r *http.Request) {
// 	if r.Method != http.MethodPost {
// 		http.Error(w, "use POST", http.StatusMethodNotAllowed)
// 		return
// 	}
// 	var body struct {
// 		DeviceID string `json:"device_id"`
// 		Name     string `json:"name"`
// 		Secret   string `json:"secret"`
// 	}
// 	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
// 		http.Error(w, "bad json", http.StatusBadRequest)
// 		return
// 	}
// 	if body.DeviceID == "" {
// 		http.Error(w, "missing device_id", http.StatusBadRequest)
// 		return
// 	}
// 	if sharedSecret != "" && body.Secret != sharedSecret {
// 		http.Error(w, "bad secret", http.StatusForbidden)
// 		return
// 	}
// 	devicesMu.Lock()
// 	defer devicesMu.Unlock()
// 	dev := devices[body.DeviceID]
// 	if dev == nil {
// 		dev = &Device{ID: body.DeviceID, Name: body.Name, LastSeen: time.Now().Unix(), Queue: []Command{}}
// 		devices[body.DeviceID] = dev
// 	} else {
// 		dev.Name = body.Name
// 		dev.LastSeen = time.Now().Unix()
// 	}
// 	log.Printf("Registered device: %s (%s)\n", body.DeviceID, body.Name)
// 	w.WriteHeader(200)
// 	io.WriteString(w, `{"ok":true}`)
// }

// func handlePoll(w http.ResponseWriter, r *http.Request) {
// 	deviceID := r.URL.Query().Get("device_id")
// 	secret := r.URL.Query().Get("secret")
// 	if deviceID == "" {
// 		http.Error(w, "missing device_id", http.StatusBadRequest)
// 		return
// 	}
// 	if sharedSecret != "" && secret != sharedSecret {
// 		http.Error(w, "bad secret", http.StatusForbidden)
// 		return
// 	}
// 	devicesMu.RLock()
// 	dev := devices[deviceID]
// 	devicesMu.RUnlock()
// 	if dev == nil {
// 		http.Error(w, "unknown device", http.StatusNotFound)
// 		return
// 	}
// 	// update last seen (protected by lock for safety)
// 	dev.QueueLock.Lock()
// 	dev.LastSeen = time.Now().Unix()
// 	// pop one command from queue if any
// 	var cmd Command
// 	if len(dev.Queue) > 0 {
// 		cmd = dev.Queue[0]
// 		dev.Queue = dev.Queue[1:]
// 	}
// 	dev.QueueLock.Unlock()

// 	resp := map[string]string{"id": cmd.ID, "cmd": cmd.Cmd}
// 	w.Header().Set("Content-Type", "application/json")
// 	_ = json.NewEncoder(w).Encode(resp)
// }

// func handleResult(w http.ResponseWriter, r *http.Request) {
// 	if r.Method != http.MethodPost {
// 		http.Error(w, "use POST", http.StatusMethodNotAllowed)
// 		return
// 	}
// 	var body struct {
// 		DeviceID string `json:"device_id"`
// 		Secret   string `json:"secret"`
// 		CmdID    string `json:"cmd_id"`
// 		Output   string `json:"output"`
// 	}
// 	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
// 		http.Error(w, "bad json", http.StatusBadRequest)
// 		return
// 	}
// 	if sharedSecret != "" && body.Secret != sharedSecret {
// 		http.Error(w, "bad secret", http.StatusForbidden)
// 		return
// 	}

// 	// Дедуп результатов по cmd_id
// 	if body.CmdID != "" {
// 		recentResultsMu.Lock()
// 		seenAt, seen := recentResults[body.CmdID]
// 		now := time.Now().Unix()
// 		if seen && (now-seenAt) < resultDuplicateWindowSec {
// 			// игнорируем дубликат
// 			recentResultsMu.Unlock()
// 			w.WriteHeader(200)
// 			io.WriteString(w, `{"ok":true,"note":"duplicate ignored"}`)
// 			log.Printf("Ignored duplicate result cmd_id=%s from device=%s\n", body.CmdID, body.DeviceID)
// 			return
// 		}
// 		// запомнить/обновить отметку
// 		recentResults[body.CmdID] = now
// 		// чистка старых записей (лениво, периодически)
// 		if len(recentResults) > 10000 {
// 			// простая очистка — удалить старые
// 			for k, t := range recentResults {
// 				if now-t > resultDuplicateWindowSec*10 {
// 					delete(recentResults, k)
// 				}
// 			}
// 		}
// 		recentResultsMu.Unlock()
// 	}

// 	// forward result to telegram user (with message throttling)
// 	text := fmt.Sprintf("Result from %s (cmd_id=%s):\n%s", body.DeviceID, body.CmdID, body.Output)
// 	sendMessage(allowID, text)

// 	w.WriteHeader(200)
// 	io.WriteString(w, `{"ok":true}`)
// }

// func handleDevices(w http.ResponseWriter, r *http.Request) {
// 	devicesMu.RLock()
// 	defer devicesMu.RUnlock()
// 	_ = json.NewEncoder(w).Encode(devices)
// }

// // === Manager functions ===

// func sendMessage(chatID int64, text string) {
// 	// throttle identical messages: if same text was sent < messageThrottleSec seconds ago -> skip
// 	now := time.Now().Unix()
// 	lastSentMapMu.Lock()
// 	last, ok := lastSentMap[text]
// 	if ok && (now-last) < messageThrottleSec {
// 		lastSentMapMu.Unlock()
// 		log.Printf("Throttled identical message to %d (skipped): %.100s\n", chatID, text)
// 		return
// 	}
// 	lastSentMap[text] = now
// 	lastSentMapMu.Unlock()

// 	form := url.Values{}
// 	form.Add("chat_id", fmt.Sprint(chatID))
// 	form.Add("text", text)
// 	// attempt send (ignore error but log)
// 	resp, err := http.PostForm(apiBase+"sendMessage", form)
// 	if err != nil {
// 		log.Printf("sendMessage error: %v\n", err)
// 		return
// 	}
// 	body, _ := io.ReadAll(resp.Body)
// 	resp.Body.Close()
// 	// log Telegram response for debugging
// 	log.Printf("sendMessage -> chat=%d len_text=%d resp_status=%s resp_body=%.200s\n", chatID, len(text), resp.Status, string(body))
// }

// // Fixed telegram loop: use update_id from response to advance offset properly.
// // Also add logging so мы видим входящие сообщения.
// func telegramLoop() {
// 	offset := 0
// 	for {
// 		u := fmt.Sprintf("%sgetUpdates?timeout=30&offset=%d", apiBase, offset)
// 		resp, err := http.Get(u)
// 		if err != nil {
// 			log.Println("getUpdates error:", err)
// 			time.Sleep(2 * time.Second)
// 			continue
// 		}
// 		body, _ := io.ReadAll(resp.Body)
// 		resp.Body.Close()

// 		var res struct {
// 			OK     bool `json:"ok"`
// 			Result []struct {
// 				UpdateID int                    `json:"update_id"`
// 				Message  map[string]interface{} `json:"message"`
// 			} `json:"result"`
// 		}
// 		if err := json.Unmarshal(body, &res); err != nil || !res.OK {
// 			log.Println("getUpdates: bad parse or ok=false")
// 			time.Sleep(1 * time.Second)
// 			continue
// 		}
// 		for _, upd := range res.Result {
// 			// advance offset to update_id+1 to avoid reprocessing
// 			offset = upd.UpdateID + 1

// 			msg := upd.Message
// 			if msg == nil {
// 				continue
// 			}
// 			from, ok := msg["from"].(map[string]interface{})
// 			if !ok {
// 				continue
// 			}
// 			// from.id is float64 in JSON
// 			fromIDf, ok := from["id"].(float64)
// 			if !ok {
// 				continue
// 			}
// 			fromID := int64(fromIDf)
// 			chat := msg["chat"].(map[string]interface{})
// 			chatIDf := chat["id"].(float64)
// 			chatID := int64(chatIDf)

// 			// debug log
// 			if t, ok := msg["text"].(string); ok {
// 				log.Printf("Incoming message from %d: %s\n", fromID, strings.SplitN(t, "\n", 2)[0])
// 			}

// 			if fromID != allowID {
// 				// ignore others
// 				continue
// 			}
// 			text, _ := msg["text"].(string)
// 			if text == "" {
// 				continue
// 			}
// 			handleTelegramCommand(chatID, text)
// 		}
// 		// small sleep to avoid busy loop
// 		time.Sleep(200 * time.Millisecond)
// 	}
// }

// // telegram command handler unchanged logic (keep same as previous implementation)
// func handleTelegramCommand(chatID int64, text string) {
// 	fields := splitFields(text)
// 	if len(fields) == 0 {
// 		sendMessage(chatID, "Empty command. Use 'help' for commands.")
// 		return
// 	}
// 	switch fields[0] {
// 	case "help":
// 		sendMessage(chatID, "Commands:\nlist\nexec <device_id> <command...>\nname <device_id> <new name>\nhelp")
// 	case "list":
// 		devicesMu.RLock()
// 		if len(devices) == 0 {
// 			devicesMu.RUnlock()
// 			sendMessage(chatID, "No devices registered.")
// 			return
// 		}
// 		var b bytes.Buffer
// 		for id, d := range devices {
// 			b.WriteString(fmt.Sprintf("- %s (name=%s) last_seen=%s queue=%d\n", id, d.Name, time.Unix(d.LastSeen, 0).Format(time.RFC3339), len(d.Queue)))
// 		}
// 		devicesMu.RUnlock()
// 		sendMessage(chatID, b.String())
// 	case "exec":
// 		if len(fields) < 3 {
// 			sendMessage(chatID, "Usage: exec <device_id> <command...>")
// 			return
// 		}
// 		deviceID := fields[1]
// 		cmdText := joinFields(fields[2:])
// 		devicesMu.RLock()
// 		dev := devices[deviceID]
// 		devicesMu.RUnlock()
// 		if dev == nil {
// 			sendMessage(chatID, "Unknown device: "+deviceID)
// 			return
// 		}
// 		cmd := Command{ID: fmt.Sprintf("c-%d", time.Now().UnixNano()), Cmd: cmdText, Created: time.Now().Unix()}
// 		dev.QueueLock.Lock()
// 		dev.Queue = append(dev.Queue, cmd)
// 		dev.QueueLock.Unlock()
// 		sendMessage(chatID, fmt.Sprintf("Command queued for %s, id=%s", deviceID, cmd.ID))
// 	case "name":
// 		if len(fields) < 3 {
// 			sendMessage(chatID, "Usage: name <device_id> <new name>")
// 			return
// 		}
// 		deviceID := fields[1]
// 		newName := joinFields(fields[2:])
// 		devicesMu.Lock()
// 		dev := devices[deviceID]
// 		if dev != nil {
// 			dev.Name = newName
// 			sendMessage(chatID, "Renamed "+deviceID+" -> "+newName)
// 		} else {
// 			sendMessage(chatID, "Unknown device: "+deviceID)
// 		}
// 		devicesMu.Unlock()
// 	default:
// 		sendMessage(chatID, "Unknown command. Use 'help'.")
// 	}
// }

// func splitFields(s string) []string {
// 	return filterEmpty(stringsSplit(s))
// }
// func joinFields(parts []string) string {
// 	return joinWithSpace(parts)
// }
// func stringsSplit(s string) []string {
// 	var out []string
// 	cur := ""
// 	for i := 0; i < len(s); i++ {
// 		c := s[i]
// 		if c == ' ' || c == '\t' || c == '\n' {
// 			if cur != "" {
// 				out = append(out, cur)
// 				cur = ""
// 			}
// 			continue
// 		}
// 		cur += string(c)
// 	}
// 	if cur != "" {
// 		out = append(out, cur)
// 	}
// 	return out
// }
// func filterEmpty(arr []string) []string {
// 	out := []string{}
// 	for _, a := range arr {
// 		if a != "" {
// 			out = append(out, a)
// 		}
// 	}
// 	return out
// }
// func joinWithSpace(arr []string) string {
// 	var b bytes.Buffer
// 	for i, a := range arr {
// 		if i > 0 {
// 			b.WriteByte(' ')
// 		}
// 		b.WriteString(a)
// 	}
// 	return b.String()
// }

// manager.go (with persistent cwd per device)
// manager.go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"
)

type Command struct {
	ID      string `json:"id"`
	Cmd     string `json:"cmd"`
	Created int64  `json:"created"`
}

type Device struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Cwd       string     `json:"cwd"`
	LastSeen  int64      `json:"last_seen"`
	Queue     []Command  `json:"queue"`
	QueueLock sync.Mutex `json:"-"`
}

var (
	telegramToken = os.Getenv("TELEGRAM_BOT_TOKEN")
	allowedIDEnv  = os.Getenv("ALLOWED_USER_ID")
	passPhrase    = os.Getenv("PASS_PHRASE")
	sharedSecret  = os.Getenv("SHARED_SECRET")
	managerPort   = os.Getenv("MANAGER_PORT")

	apiBase string
	allowID int64

	devices   = map[string]*Device{}
	devicesMu sync.RWMutex

	persistFile = "devices.json"
)

func main() {
	if telegramToken == "" || allowedIDEnv == "" || passPhrase == "" {
		log.Fatal("Set TELEGRAM_BOT_TOKEN, ALLOWED_USER_ID and PASS_PHRASE environment variables")
	}
	var err error
	allowID, err = strconv.ParseInt(allowedIDEnv, 10, 64)
	if err != nil {
		log.Fatalf("Bad ALLOWED_USER_ID: %v", err)
	}
	if managerPort == "" {
		managerPort = "8080"
	}
	apiBase = "https://api.telegram.org/bot" + telegramToken + "/"

	// load persisted devices (if any)
	loadDevices()

	// HTTP endpoints
	http.HandleFunc("/register", handleRegister)
	http.HandleFunc("/poll", handlePoll)
	http.HandleFunc("/result", handleResult)
	http.HandleFunc("/devices", handleDevices)

	serverAddr := ":" + managerPort
	go func() {
		log.Printf("Starting HTTP server at %s\n", serverAddr)
		if err := http.ListenAndServe(serverAddr, nil); err != nil {
			log.Fatalf("ListenAndServe: %v", err)
		}
	}()

	go telegramLoop()

	sendMessage(allowID, fmt.Sprintf("Manager started at %s (port %s). Shared secret: %v", time.Now().Format(time.RFC3339), managerPort, sharedSecret != ""))

	select {}
}

// Persistence
func saveDevices() {
	devicesMu.RLock()
	defer devicesMu.RUnlock()
	f, err := os.Create(persistFile + ".tmp")
	if err != nil {
		log.Printf("saveDevices: create tmp error: %v\n", err)
		return
	}
	enc := json.NewEncoder(f)
	if err := enc.Encode(devices); err != nil {
		log.Printf("saveDevices: encode error: %v\n", err)
		f.Close()
		return
	}
	f.Close()
	_ = os.Rename(persistFile+".tmp", persistFile)
}

func loadDevices() {
	f, err := os.Open(persistFile)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("loadDevices: open error: %v\n", err)
		}
		return
	}
	defer f.Close()
	var loaded map[string]*Device
	if err := json.NewDecoder(f).Decode(&loaded); err != nil {
		log.Printf("loadDevices: decode error: %v\n", err)
		return
	}
	devicesMu.Lock()
	for k, v := range loaded {
		devices[k] = v
		// ensure QueueLock zero value ok
	}
	devicesMu.Unlock()
	log.Printf("Loaded %d devices from %s\n", len(devices), persistFile)
}

// Handlers
func handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "use POST", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		DeviceID string `json:"device_id"`
		Name     string `json:"name"`
		Secret   string `json:"secret"`
		Cwd      string `json:"cwd"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if body.DeviceID == "" {
		http.Error(w, "missing device_id", http.StatusBadRequest)
		return
	}
	if sharedSecret != "" && body.Secret != sharedSecret {
		http.Error(w, "bad secret", http.StatusForbidden)
		return
	}
	devicesMu.Lock()
	dev := devices[body.DeviceID]
	if dev == nil {
		dev = &Device{ID: body.DeviceID, Name: body.Name, Cwd: body.Cwd, LastSeen: time.Now().Unix(), Queue: []Command{}}
		devices[body.DeviceID] = dev
	} else {
		dev.Name = body.Name
		dev.Cwd = body.Cwd
		dev.LastSeen = time.Now().Unix()
	}
	devicesMu.Unlock()
	saveDevices()
	log.Printf("Registered device: %s (%s) cwd=%s\n", body.DeviceID, body.Name, body.Cwd)
	w.WriteHeader(200)
	io.WriteString(w, `{"ok":true}`)
}

func handlePoll(w http.ResponseWriter, r *http.Request) {
	deviceID := r.URL.Query().Get("device_id")
	secret := r.URL.Query().Get("secret")
	if deviceID == "" {
		http.Error(w, "missing device_id", http.StatusBadRequest)
		return
	}
	if sharedSecret != "" && secret != sharedSecret {
		http.Error(w, "bad secret", http.StatusForbidden)
		return
	}
	devicesMu.RLock()
	dev := devices[deviceID]
	devicesMu.RUnlock()
	if dev == nil {
		http.Error(w, "unknown device", http.StatusNotFound)
		return
	}
	// update last seen
	dev.QueueLock.Lock()
	dev.LastSeen = time.Now().Unix()
	// pop one command
	var cmd Command
	if len(dev.Queue) > 0 {
		cmd = dev.Queue[0]
		dev.Queue = dev.Queue[1:]
	}
	dev.QueueLock.Unlock()

	resp := map[string]string{"id": cmd.ID, "cmd": cmd.Cmd}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
	// persist queue change
	saveDevices()
}

func handleResult(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "use POST", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		DeviceID string `json:"device_id"`
		Secret   string `json:"secret"`
		CmdID    string `json:"cmd_id"`
		Output   string `json:"output"`
		Cwd      string `json:"cwd"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if sharedSecret != "" && body.Secret != sharedSecret {
		http.Error(w, "bad secret", http.StatusForbidden)
		return
	}
	// update device last seen and cwd
	devicesMu.Lock()
	dev := devices[body.DeviceID]
	if dev != nil {
		dev.LastSeen = time.Now().Unix()
		if body.Cwd != "" && body.Cwd != dev.Cwd {
			dev.Cwd = body.Cwd
			log.Printf("Updated cwd for %s -> %s\n", body.DeviceID, dev.Cwd)
		}
	}
	devicesMu.Unlock()
	saveDevices()

	// Forward result to telegram user (shorten long outputs)
	out := body.Output
	if len(out) > 4000 {
		out = out[:4000] + "\n...truncated..."
	}
	text := fmt.Sprintf("Result from %s (cmd_id=%s) cwd=%s:\n%s", body.DeviceID, body.CmdID, body.Cwd, out)
	sendMessage(allowID, text)

	w.WriteHeader(200)
	io.WriteString(w, `{"ok":true}`)
}

func handleDevices(w http.ResponseWriter, r *http.Request) {
	devicesMu.RLock()
	defer devicesMu.RUnlock()
	_ = json.NewEncoder(w).Encode(devices)
}

// Telegram support (simple)
func sendMessage(chatID int64, text string) {
	form := url.Values{}
	form.Add("chat_id", fmt.Sprint(chatID))
	form.Add("text", text)
	_, err := http.PostForm(apiBase+"sendMessage", form)
	if err != nil {
		log.Printf("sendMessage error: %v\n", err)
	}
}

func telegramLoop() {
	offset := 0
	for {
		u := fmt.Sprintf("%sgetUpdates?timeout=30&offset=%d", apiBase, offset)
		resp, err := http.Get(u)
		if err != nil {
			log.Println("getUpdates error:", err)
			time.Sleep(2 * time.Second)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var res struct {
			OK     bool `json:"ok"`
			Result []struct {
				UpdateID int                    `json:"update_id"`
				Message  map[string]interface{} `json:"message"`
			} `json:"result"`
		}
		if err := json.Unmarshal(body, &res); err != nil || !res.OK {
			time.Sleep(1 * time.Second)
			continue
		}
		for _, upd := range res.Result {
			offset = upd.UpdateID + 1
			msg := upd.Message
			if msg == nil {
				continue
			}
			from, ok := msg["from"].(map[string]interface{})
			if !ok {
				continue
			}
			fromIDf, ok := from["id"].(float64)
			if !ok {
				continue
			}
			fromID := int64(fromIDf)
			chat := msg["chat"].(map[string]interface{})
			chatIDf := chat["id"].(float64)
			chatID := int64(chatIDf)
			if fromID != allowID {
				continue
			}
			text, _ := msg["text"].(string)
			if text == "" {
				continue
			}
			handleTelegramCommand(chatID, text)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func handleTelegramCommand(chatID int64, text string) {
	fields := splitFields(text)
	if len(fields) == 0 {
		sendMessage(chatID, "Empty command. Use 'help'.")
		return
	}
	switch fields[0] {
	case "help":
		sendMessage(chatID, "Commands:\nlist\nexec <device_id> <command...>\nname <device_id> <new name>\nhelp")
	case "list":
		devicesMu.RLock()
		if len(devices) == 0 {
			devicesMu.RUnlock()
			sendMessage(chatID, "No devices registered.")
			return
		}
		var b bytes.Buffer
		for id, d := range devices {
			b.WriteString(fmt.Sprintf("- %s (name=%s) cwd=%s last_seen=%s queue=%d\n", id, d.Name, d.Cwd, time.Unix(d.LastSeen, 0).Format(time.RFC3339), len(d.Queue)))
		}
		devicesMu.RUnlock()
		sendMessage(chatID, b.String())
	case "exec":
		if len(fields) < 3 {
			sendMessage(chatID, "Usage: exec <device_id> <command...>")
			return
		}
		deviceID := fields[1]
		cmdText := joinFields(fields[2:])
		devicesMu.RLock()
		dev := devices[deviceID]
		devicesMu.RUnlock()
		if dev == nil {
			sendMessage(chatID, "Unknown device: "+deviceID)
			return
		}
		cmd := Command{ID: fmt.Sprintf("c-%d", time.Now().UnixNano()), Cmd: cmdText, Created: time.Now().Unix()}
		dev.QueueLock.Lock()
		dev.Queue = append(dev.Queue, cmd)
		dev.QueueLock.Unlock()
		saveDevices()
		sendMessage(chatID, fmt.Sprintf("Command queued for %s, id=%s", deviceID, cmd.ID))
	case "name":
		if len(fields) < 3 {
			sendMessage(chatID, "Usage: name <device_id> <new name>")
			return
		}
		deviceID := fields[1]
		newName := joinFields(fields[2:])
		devicesMu.Lock()
		dev := devices[deviceID]
		if dev != nil {
			dev.Name = newName
			saveDevices()
			sendMessage(chatID, "Renamed "+deviceID+" -> "+newName)
		} else {
			sendMessage(chatID, "Unknown device: "+deviceID)
		}
		devicesMu.Unlock()
	default:
		sendMessage(chatID, "Unknown command. Use 'help'.")
	}
}

// small helpers
func splitFields(s string) []string {
	out := []string{}
	cur := ""
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ' ' || c == '\t' || c == '\n' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			continue
		}
		cur += string(c)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
func joinFields(parts []string) string {
	s := ""
	for i, p := range parts {
		if i > 0 {
			s += " "
		}
		s += p
	}
	return s
}
