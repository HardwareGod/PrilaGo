package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
)

var db *sql.DB

type OpenAIRequest struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
}

type OpenAIResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func SendMessage(message string) (string, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return "", fmt.Errorf("API key not set. Please set the OPENAI_API_KEY environment variable")
	}

	reqData := OpenAIRequest{
		Model: "gpt-3.5-turbo",
		Messages: []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}{
			{
				Role:    "system",
				Content: "Вы находитесь в мире Гарри Поттера. Общайтесь вежливо на 'вы' и используйте речевые обороты, характерные для книг и фильмов о Гарри Поттере.",
			},
			{
				Role:    "user",
				Content: message,
			},
		},
	}

	reqBody, err := json.Marshal(reqData)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request data: %v", err)
	}

	client := &http.Client{}
	req, err := http.NewRequest("POST", "https://api.openai.com/v1/chat/completions", bytes.NewBuffer(reqBody))
	if err != nil {
		return "", fmt.Errorf("failed to create HTTP request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	fmt.Println("Отправка запроса в OpenAI...")

	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("Ошибка при отправке запроса: %v\n", err)
		return "", err
	}
	defer resp.Body.Close()

	fmt.Println("Ответ получен от OpenAI")

	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("Ошибка при чтении тела ответа: %v\n", err)
		return "", err
	}

	var response OpenAIResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		fmt.Printf("Ошибка при декодировании ответа: %v\n", err)
		return "", err
	}

	if len(response.Choices) == 0 {
		fmt.Println("Ответ не содержит выборов")
		return "", fmt.Errorf("no choices returned from OpenAI")
	}
	return response.Choices[0].Message.Content, nil
}

func handleRequest(w http.ResponseWriter, r *http.Request) {
	// Извлекаем или создаем пользователя
	userID, err := getUserID(w, r)
	if err != nil {
		http.Error(w, "Ошибка идентификации пользователя", http.StatusInternalServerError)
		return
	}

	// Читаем запрос от клиента
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Ошибка чтения данных", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var userInput struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &userInput); err != nil {
		http.Error(w, "Ошибка обработки данных", http.StatusBadRequest)
		return
	}

	// Отправляем сообщение в OpenAI
	response, err := SendMessage(userInput.Message)
	if err != nil {
		http.Error(w, "Ошибка при обращении к OpenAI", http.StatusInternalServerError)
		return
	}

	// Сохраняем сообщение и ответ в базе данных
	err = saveMessage(userID, userInput.Message, response)
	if err != nil {
		fmt.Printf("Ошибка при сохранении сообщения: %v\n", err)
	}

	// Отправляем ответ клиенту
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}{
		Choices: []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}{
			{
				Message: struct {
					Content string `json:"content"`
				}{
					Content: response,
				},
			},
		},
	})
}

func handleHistory(w http.ResponseWriter, r *http.Request) {
	userID, err := getUserID(w, r)
	if err != nil {
		http.Error(w, "Ошибка идентификации пользователя", http.StatusInternalServerError)
		return
	}

	rows, err := db.Query("SELECT user_message, bot_response, timestamp FROM messages WHERE user_id = ? ORDER BY timestamp ASC", userID)
	if err != nil {
		http.Error(w, "Ошибка при получении истории", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var history []struct {
		UserMessage string `json:"user_message"`
		BotResponse string `json:"bot_response"`
		Timestamp   string `json:"timestamp"`
	}

	for rows.Next() {
		var userMsg, botResp, timestamp string
		if err := rows.Scan(&userMsg, &botResp, &timestamp); err != nil {
			continue
		}
		history = append(history, struct {
			UserMessage string `json:"user_message"`
			BotResponse string `json:"bot_response"`
			Timestamp   string `json:"timestamp"`
		}{
			UserMessage: userMsg,
			BotResponse: botResp,
			Timestamp:   timestamp,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(history)
}

func main() {
	var err error
	db, err = sql.Open("sqlite3", "./chat.db")
	if err != nil {
		fmt.Printf("Ошибка при подключении к базе данных: %v\n", err)
		return
	}
	defer db.Close()

	// Инициализируем таблицы
	err = initializeDatabase()
	if err != nil {
		fmt.Printf("Ошибка при инициализации базы данных: %v\n", err)
		return
	}

	http.HandleFunc("/chat", handleRequest)
	http.HandleFunc("/history", handleHistory)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "index.html")
	})

	fmt.Println("Сервер запущен на http://localhost:8080")
	err = http.ListenAndServe(":8080", nil)
	if err != nil {
		fmt.Printf("Ошибка при запуске сервера: %v\n", err)
	}
}

func initializeDatabase() error {
	userTable := `
    CREATE TABLE IF NOT EXISTS users (
        id TEXT PRIMARY KEY,
        created_at DATETIME DEFAULT CURRENT_TIMESTAMP
    );`

	messageTable := `
    CREATE TABLE IF NOT EXISTS messages (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        user_id TEXT,
        user_message TEXT,
        bot_response TEXT,
        timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
        FOREIGN KEY(user_id) REFERENCES users(id)
    );`

	_, err := db.Exec(userTable)
	if err != nil {
		return fmt.Errorf("failed to create users table: %v", err)
	}

	_, err = db.Exec(messageTable)
	if err != nil {
		return fmt.Errorf("failed to create messages table: %v", err)
	}

	return nil
}

func getUserID(w http.ResponseWriter, r *http.Request) (string, error) {
	cookie, err := r.Cookie("user_id")
	if err != nil {
		// Если куки нет, создаем нового пользователя
		newUserID := uuid.New().String()
		_, err := db.Exec("INSERT INTO users (id) VALUES (?)", newUserID)
		if err != nil {
			return "", fmt.Errorf("failed to insert new user: %v", err)
		}
		// Устанавливаем куки
		http.SetCookie(w, &http.Cookie{
			Name:    "user_id",
			Value:   newUserID,
			Expires: time.Now().Add(365 * 24 * time.Hour),
			Path:    "/",
		})
		return newUserID, nil
	}
	return cookie.Value, nil
}

func saveMessage(userID, userMessage, botResponse string) error {
	_, err := db.Exec("INSERT INTO messages (user_id, user_message, bot_response) VALUES (?, ?, ?)", userID, userMessage, botResponse)
	if err != nil {
		return fmt.Errorf("failed to save message: %v", err)
	}
	return nil
}
