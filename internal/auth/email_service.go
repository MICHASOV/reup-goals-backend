package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"reup-goals-backend/internal/config"
)

const defaultServiceListTitle = "REUP.goals service emails"

var (
	errEmailServiceNotConfigured = errors.New("email service not configured")
	errEmailListUnavailable      = errors.New("email_list_unavailable")
)

type EmailService struct {
	apiKey           string
	baseURL          string
	senderEmail      string
	senderName       string
	listID           string
	serviceListTitle string
	client           *http.Client
	mu               sync.Mutex
}

type unisenderResponse struct {
	Result json.RawMessage `json:"result"`
	Error  string          `json:"error"`
	Code   string          `json:"code"`
}

type unisenderList struct {
	ID    any    `json:"id"`
	Title string `json:"title"`
}

func NewEmailService(cfg *config.Config) *EmailService {
	return &EmailService{
		apiKey:           strings.TrimSpace(cfg.UnisenderAPIKey),
		baseURL:          strings.TrimRight(strings.TrimSpace(cfg.UnisenderBaseURL), "/"),
		senderEmail:      strings.TrimSpace(cfg.UnisenderSenderEmail),
		senderName:       strings.TrimSpace(cfg.UnisenderSenderName),
		listID:           strings.TrimSpace(cfg.UnisenderListID),
		serviceListTitle: strings.TrimSpace(cfg.UnisenderServiceListTitle),
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (s *EmailService) SendVerificationCode(email, code string) error {
	return s.sendEmail(
		email,
		"Код подтверждения REUP.goals",
		fmt.Sprintf(
			`<p>Ваш код подтверждения:</p><p style="font-size:24px;font-weight:700;">%s</p><p>Введите его на сайте REUP.goals.</p><p>Код действует 15 минут.</p>`,
			code,
		),
	)
}

func (s *EmailService) SendPasswordResetCode(email, code string) error {
	return s.sendEmail(
		email,
		"Код для восстановления пароля REUP.goals",
		fmt.Sprintf(
			`<p>Ваш код для восстановления пароля:</p><p style="font-size:24px;font-weight:700;">%s</p><p>Введите его на сайте REUP.goals.</p><p>Код действует 15 минут.</p><p>Если вы не запрашивали восстановление пароля, просто проигнорируйте это письмо.</p>`,
			code,
		),
	)
}

func (s *EmailService) sendEmail(email, subject, body string) error {
	if err := s.validateConfig(); err != nil {
		return err
	}

	listID, err := s.ensureListID()
	if err != nil {
		return err
	}

	values := url.Values{}
	values.Set("api_key", s.apiKey)
	values.Set("email", email)
	values.Set("sender_name", s.senderName)
	values.Set("sender_email", s.senderEmail)
	values.Set("subject", subject)
	values.Set("body", body)
	values.Set("list_id", listID)
	values.Set("lang", "ru")
	values.Set("error_checking", "1")

	var response unisenderResponse
	if err := s.post("sendEmail", values, &response); err != nil {
		return err
	}

	if response.Error != "" || response.Code != "" {
		return fmt.Errorf("unisender send failed: %s", response.Code)
	}
	if len(response.Result) == 0 || string(response.Result) == "null" {
		return errors.New("unisender send failed")
	}

	return nil
}

func (s *EmailService) validateConfig() error {
	if s.apiKey == "" || s.senderEmail == "" || s.senderName == "" || s.baseURL == "" {
		return errEmailServiceNotConfigured
	}
	if s.serviceListTitle == "" {
		s.serviceListTitle = defaultServiceListTitle
	}
	return nil
}

func (s *EmailService) ensureListID() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.listID != "" {
		return s.listID, nil
	}

	listID, err := s.findListID()
	if err == nil && listID != "" {
		s.listID = listID
		return s.listID, nil
	}

	if err := s.createList(); err != nil {
		listID, findErr := s.findListID()
		if findErr == nil && listID != "" {
			s.listID = listID
			return s.listID, nil
		}
		return "", errEmailListUnavailable
	}

	listID, err = s.findListID()
	if err != nil || listID == "" {
		return "", errEmailListUnavailable
	}

	s.listID = listID
	return s.listID, nil
}

func (s *EmailService) findListID() (string, error) {
	values := url.Values{}
	values.Set("api_key", s.apiKey)

	var response struct {
		Result []unisenderList `json:"result"`
		Error  string          `json:"error"`
		Code   string          `json:"code"`
	}
	if err := s.post("getLists", values, &response); err != nil {
		return "", err
	}
	if response.Error != "" || response.Code != "" {
		return "", errEmailListUnavailable
	}

	for _, list := range response.Result {
		if list.Title == s.serviceListTitle {
			return fmt.Sprint(list.ID), nil
		}
	}

	return "", nil
}

func (s *EmailService) createList() error {
	values := url.Values{}
	values.Set("api_key", s.apiKey)
	values.Set("title", s.serviceListTitle)

	var response unisenderResponse
	if err := s.post("createList", values, &response); err != nil {
		return err
	}
	if response.Error != "" || response.Code != "" {
		return errEmailListUnavailable
	}
	if len(response.Result) == 0 || string(response.Result) == "null" {
		return errEmailListUnavailable
	}

	return nil
}

func (s *EmailService) post(method string, values url.Values, out any) error {
	requestURL := s.baseURL + "/" + method + "?format=json"
	resp, err := s.client.PostForm(requestURL, values)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("unisender http status: %d", resp.StatusCode)
	}

	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}
