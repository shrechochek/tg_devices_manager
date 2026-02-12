package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Структуры для парсинга ответов Telegram
type Update struct {
	UpdateID int `json:"update_id"`
	Message  struct {
		Text string `json:"text"`
		Chat struct {
			ID int `json:"id"`
		} `json:"chat"`
	} `json:"message"`
}

type TelegramResponse struct {
	Ok     bool     `json:"ok"`
	Result []Update `json:"result"`
}

func main() {
	// СОВЕТ: Никогда не храни токен прямо в коде. Используй переменные окружения.
	botToken := "8370332280:AAETBUy-XoUfGX9S-XcYsMjOzEkD6aqnaJs"
	offset := 0

	fmt.Println("Бот-терминал запущен...")

	for {
		// Используем Long Polling (timeout=30), чтобы не спамить запросами
		updatesUrl := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?offset=%d&timeout=30", botToken, offset)

		resp, err := http.Get(updatesUrl)
		if err != nil {
			fmt.Println("Ошибка сети:", err)
			time.Sleep(2 * time.Second)
			continue
		}

		var data TelegramResponse
		if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
			fmt.Println("Ошибка декодирования JSON:", err)
			resp.Body.Close()
			continue
		}
		resp.Body.Close()

		for _, update := range data.Result {
			offset = update.UpdateID + 1
			chatID := update.Message.Chat.ID
			text := update.Message.Text

			if text == "" {
				continue
			}

			fmt.Printf("Выполняю команду: %s\n", text)

			// Обрабатываем команду и получаем результат
			output := handleCommand(text)

			// Отправляем результат обратно в Telegram
			sendMessage(botToken, chatID, output)
		}
	}
}

// handleCommand разбирает текст и выполняет его в ОС
func handleCommand(input string) string {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return "Пустая команда"
	}

	command := parts[0]
	args := parts[1:]

	// Специфическая обработка cd
	if command == "cd" {
		target := "."
		if len(args) > 0 {
			target = args[0]
		}
		if err := os.Chdir(target); err != nil {
			return fmt.Sprintf("Ошибка cd: %v", err)
		}
		currDir, _ := os.Getwd()
		return fmt.Sprintf("Директория изменена на: %s", currDir)
	}

	// Выполнение системных команд
	cmd := exec.Command(command, args...)

	// Захватываем и stdout и stderr в один буфер
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Run()
	result := out.String()

	if result == "" {
		if err != nil {
			return fmt.Sprintf("Ошибка: %v", err)
		}
		return "Команда выполнена (пустой вывод)"
	}

	currDir, _ := os.Getwd()
	result = fmt.Sprintf("[%s]\n%s", currDir, result)

	return result
}

// sendMessage отправляет текст пользователю через API Telegram
func sendMessage(token string, chatID int, text string) {
	// Если вывод слишком длинный, Telegram его не примет (лимит ~4096 символов)
	if len(text) > 4000 {
		text = text[:4000] + "... (вывод обрезан)"
	}

	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)

	// Отправляем данные как URL-encoded (проще всего для текста)
	formData := url.Values{}
	formData.Set("chat_id", strconv.Itoa(chatID))
	formData.Set("text", "```\n"+text+"\n```") // Оформляем как код
	formData.Set("parse_mode", "MarkdownV2")

	_, err := http.PostForm(apiURL, formData)
	if err != nil {
		fmt.Printf("Ошибка при отправке сообщения: %v\n", err)
	}
}
