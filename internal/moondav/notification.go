package moondav

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"strings"
	"time"
)

type notificationEvent struct {
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	Message string `json:"message"`
	Time    string `json:"time"`
}

func (a *App) notify(kind, title, message string) {
	if a.cfg.TelegramBotToken == "" && a.cfg.WebhookURL == "" && a.cfg.SMTPHost == "" {
		return
	}
	ev := notificationEvent{Kind: kind, Title: title, Message: message, Time: time.Now().UTC().Format(time.RFC3339)}
	if err := a.notifyTelegram(ev); err != nil {
		logNotificationError("telegram", err)
	}
	if err := a.notifyWebhook(ev); err != nil {
		logNotificationError("webhook", err)
	}
	if err := a.notifySMTP(ev); err != nil {
		logNotificationError("smtp", err)
	}
}

func logNotificationError(channel string, err error) {
	fmt.Printf("notification %s failed: %v\n", channel, err)
}

func (a *App) notifyTelegram(ev notificationEvent) error {
	if a.cfg.TelegramBotToken == "" || a.cfg.TelegramChatID == "" {
		return nil
	}
	form := url.Values{}
	form.Set("chat_id", a.cfg.TelegramChatID)
	form.Set("text", ev.Title+"\n\n"+ev.Message)
	req, err := http.NewRequest(http.MethodPost,
		"https://api.telegram.org/bot"+url.PathEscape(a.cfg.TelegramBotToken)+"/sendMessage",
		strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func (a *App) notifyWebhook(ev notificationEvent) error {
	if a.cfg.WebhookURL == "" {
		return nil
	}
	body, _ := json.Marshal(ev)
	req, err := http.NewRequest(http.MethodPost, a.cfg.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if a.cfg.WebhookBearer != "" {
		req.Header.Set("Authorization", "Bearer "+a.cfg.WebhookBearer)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func (a *App) notifySMTP(ev notificationEvent) error {
	if a.cfg.SMTPHost == "" || a.cfg.SMTPFrom == "" || a.cfg.SMTPTo == "" {
		return nil
	}
	addr := fmt.Sprintf("%s:%d", a.cfg.SMTPHost, a.cfg.SMTPPort)
	var conn net.Conn
	var err error
	if a.cfg.SMTPTLSMode == "tls" {
		conn, err = tls.Dial("tcp", addr, &tls.Config{ServerName: a.cfg.SMTPHost, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = net.DialTimeout("tcp", addr, 10*time.Second)
	}
	if err != nil {
		return err
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, a.cfg.SMTPHost)
	if err != nil {
		return err
	}
	defer client.Close()

	if a.cfg.SMTPTLSMode == "starttls" {
		if err := client.StartTLS(&tls.Config{ServerName: a.cfg.SMTPHost, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}
	if a.cfg.SMTPUser != "" {
		if err := client.Auth(smtp.PlainAuth("", a.cfg.SMTPUser, a.cfg.SMTPPassword, a.cfg.SMTPHost)); err != nil {
			return err
		}
	}
	if err := client.Mail(a.cfg.SMTPFrom); err != nil {
		return err
	}
	for _, recipient := range strings.Split(a.cfg.SMTPTo, ",") {
		if err := client.Rcpt(strings.TrimSpace(recipient)); err != nil {
			return err
		}
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	msg := "From: " + a.cfg.SMTPFrom + "\r\n" +
		"To: " + a.cfg.SMTPTo + "\r\n" +
		"Subject: " + ev.Title + "\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n\r\n" +
		ev.Message + "\r\n"
	if _, err := w.Write([]byte(msg)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}
