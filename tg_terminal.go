package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Update struct {
	UpdateID int64   `json:"update_id"`
	Message  Message `json:"message"`
}

type Message struct {
	MessageID int64       `json:"message_id"`
	From      User        `json:"from"`
	Chat      Chat        `json:"chat"`
	Text      string      `json:"text"`
	Document  *Document   `json:"document,omitempty"`
	Photo     interface{} `json:"photo,omitempty"` // optional
}

type User struct {
	ID int64 `json:"id"`
}

type Chat struct {
	ID int64 `json:"id"`
}

type Document struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
}

type GetFileResponse struct {
	OK     bool `json:"ok"`
	Result struct {
		FilePath string `json:"file_path"`
	} `json:"result"`
}

var (
	botToken      = os.Getenv("TELEGRAM_BOT_TOKEN")
	allowedID     int64
	passPhrase    = os.Getenv("PASS_PHRASE")
	currentDir    = ""
	apiBase       = ""
	authenticated = false
)

func main() {
	if botToken == "" || os.Getenv("ALLOWED_USER_ID") == "" || passPhrase == "" {
		fmt.Println("Set TELEGRAM_BOT_TOKEN, ALLOWED_USER_ID, and PASS_PHRASE")
		return
	}
	fmt.Sscanf(os.Getenv("ALLOWED_USER_ID"), "%d", &allowedID)

	apiBase = "https://api.telegram.org/bot" + botToken + "/"
	home := os.Getenv("HOME")
	if home == "" {
		home = "."
	}
	currentDir = home

	fmt.Println("Bot started. Allowed user:", allowedID)

	// Send startup message
	sendMessage(allowedID, fmt.Sprintf("🟢 Bot started at %s\nCurrent dir: %s", time.Now().Format("2006-01-02 15:04:05"), currentDir))

	var offset int64 = 0

	for {
		updates := getUpdates(offset)
		for _, upd := range updates {
			offset = upd.UpdateID + 1
			if upd.Message.From.ID != allowedID {
				continue
			}
			handleMessage(upd.Message)
		}
		time.Sleep(1 * time.Second)
	}
}

// === Fetch updates ===
func getUpdates(offset int64) []Update {
	url := fmt.Sprintf("%sgetUpdates?timeout=30&offset=%d", apiBase, offset)
	resp, err := http.Get(url)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result struct {
		OK     bool     `json:"ok"`
		Result []Update `json:"result"`
	}
	json.Unmarshal(body, &result)
	fmt.Println("Got updates count:", len(result.Result))
	return result.Result
}

// === Handle messages ===
func handleMessage(msg Message) {
	fmt.Println("Received message:", msg.Text, "from user:", msg.From.ID)
	if !authenticated {
		if msg.Text == passPhrase {
			authenticated = true
			sendMessage(msg.Chat.ID, "✅ Authenticated. Current dir: "+currentDir)
		} else {
			sendMessage(msg.Chat.ID, "❌ Wrong pass phrase")
		}
		return
	}

	if msg.Document != nil {
		saveDocument(msg.Chat.ID, msg.Document)
		return
	}

	text := strings.TrimSpace(msg.Text)
	if strings.HasPrefix(text, "cd ") || text == "cd" {
		dir := ""
		if text == "cd" {
			dir = os.Getenv("HOME")
			if dir == "" {
				dir = "."
			}
		} else {
			dir = text[3:]
		}
		changeDir(msg.Chat.ID, dir)
		return
	}

	if text == "pwd" {
		sendMessage(msg.Chat.ID, currentDir)
		return
	}

	// execute command
	out := runCommand(text)
	sendMessage(msg.Chat.ID, out)
}

// === Run shell command in currentDir ===
func runCommand(cmd string) string {
	c := exec.Command("/bin/bash", "-lc", cmd)
	c.Dir = currentDir
	var out bytes.Buffer
	c.Stdout = &out
	c.Stderr = &out
	err := c.Run()
	result := out.String()
	if err != nil {
		result += "\n[error: " + err.Error() + "]"
	}
	if len(result) > 3800 {
		result = result[:3800] + "\n...output truncated..."
	}
	return result
}

// === Change directory ===
func changeDir(chatID int64, dir string) {
	path := filepath.Join(currentDir, dir)
	abs, err := filepath.Abs(path)
	if err != nil {
		sendMessage(chatID, "❌ Failed to resolve path")
		return
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		sendMessage(chatID, "❌ Directory not found")
		return
	}
	currentDir = abs
	sendMessage(chatID, "📂 Current dir: "+currentDir)
}

// === Save document ===
func saveDocument(chatID int64, doc *Document) {
	fileInfo := getFile(doc.FileID)
	if fileInfo == "" {
		sendMessage(chatID, "❌ Failed to get file info")
		return
	}
	var f GetFileResponse
	json.Unmarshal([]byte(fileInfo), &f)
	if !f.OK {
		sendMessage(chatID, "❌ Telegram returned error")
		return
	}
	fileURL := "https://api.telegram.org/file/bot" + botToken + "/" + f.Result.FilePath
	dest := filepath.Join(currentDir, sanitizeFilename(doc.FileName))
	err := downloadFile(fileURL, dest)
	if err != nil {
		sendMessage(chatID, "❌ Failed to save file: "+err.Error())
	} else {
		sendMessage(chatID, "📁 File saved: "+dest)
	}
}

// === Helper: getFile ===
func getFile(fileID string) string {
	resp, err := http.Get(apiBase + "getFile?file_id=" + fileID)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

// === Download file from URL ===
func downloadFile(url, dest string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, resp.Body)
	return err
}

// === Sanitize filename ===
func sanitizeFilename(name string) string {
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, name)
}

// === Send message to chat ===
// func sendMessage(chatID int64, text string) {
// 	form := map[string]string{
// 		"chat_id": fmt.Sprint(chatID),
// 		"text":    text,
// 	}
// 	http.PostForm(apiBase+"sendMessage", form)
// }

func sendMessage(chatID int64, text string) {
	form := url.Values{}
	form.Add("chat_id", fmt.Sprint(chatID))
	form.Add("text", text)
	http.PostForm(apiBase+"sendMessage", form)
}
