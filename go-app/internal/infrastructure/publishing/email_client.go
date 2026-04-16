package publishing

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

// SMTPClient определяет интерфейс SMTP-клиента.
// Интерфейс позволяет подменять реализацию в тестах.
type SMTPClient interface {
	// SendEmail отправляет письмо.
	SendEmail(ctx context.Context, msg *EmailMessage) error
	// Health проверяет доступность SMTP-сервера через NOOP.
	Health(ctx context.Context) error
	// Close освобождает ресурсы (no-op для stateless клиента).
	Close() error
}

// smtpDialTimeout — таймаут на установку TCP-соединения с SMTP-сервером.
const smtpDialTimeout = 10 * time.Second

// SMTPDialer реализует SMTPClient через net/smtp.
// Соединение открывается per-send (stateless), не при создании.
// Это упрощает retry-логику и избегает stale connection.
type SMTPDialer struct {
	config SMTPConfig
	logger *slog.Logger
}

// NewSMTPDialer создаёт SMTP-клиент с заданной конфигурацией.
// Соединение не устанавливается при создании.
func NewSMTPDialer(config SMTPConfig, logger *slog.Logger) SMTPClient {
	if logger == nil {
		logger = slog.Default()
	}
	if config.Port == 0 {
		config.Port = 587
	}
	return &SMTPDialer{
		config: config,
		logger: logger,
	}
}

// addr возвращает "host:port" строку для dial.
func (d *SMTPDialer) addr() string {
	return net.JoinHostPort(d.config.Host, strconv.Itoa(d.config.Port))
}

// SendEmail отправляет письмо через SMTP.
// Каждый вызов: dial → STARTTLS → AUTH → MAIL FROM → RCPT TO → DATA → QUIT.
func (d *SMTPDialer) SendEmail(ctx context.Context, msg *EmailMessage) error {
	if len(msg.To) == 0 {
		return fmt.Errorf("email: no recipients specified")
	}

	addr := d.addr()
	d.logger.DebugContext(ctx, "Connecting to SMTP server",
		slog.String("addr", addr),
		slog.Int("recipients", len(msg.To)),
	)

	// 1. Dial с таймаутом
	conn, err := net.DialTimeout("tcp", addr, smtpDialTimeout)
	if err != nil {
		return fmt.Errorf("email: dial %s: %w", addr, err)
	}

	// 2. Создать SMTP client
	client, err := smtp.NewClient(conn, d.config.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("email: smtp.NewClient: %w", err)
	}
	defer client.Close()

	// 3. STARTTLS если RequireTLS
	if d.config.RequireTLS {
		tlsCfg := &tls.Config{
			ServerName: d.config.Host,
			MinVersion: tls.VersionTLS12,
		}
		if err := client.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("email: StartTLS: %w", err)
		}
	}

	// 4. AUTH PLAIN (если указаны credentials)
	if d.config.Username != "" {
		auth := smtp.PlainAuth("", d.config.Username, d.config.Password, d.config.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("email: AUTH: %w", err)
		}
	}

	// 5. MAIL FROM
	from := msg.From
	if from == "" {
		from = d.config.From
	}
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("email: MAIL FROM <%s>: %w", from, err)
	}

	// 6. RCPT TO для каждого получателя
	for _, to := range msg.To {
		to = strings.TrimSpace(to)
		if to == "" {
			continue
		}
		if err := client.Rcpt(to); err != nil {
			return fmt.Errorf("email: RCPT TO <%s>: %w", to, err)
		}
	}

	// 7. DATA
	wc, err := client.Data()
	if err != nil {
		return fmt.Errorf("email: DATA: %w", err)
	}

	// 8. Собрать MIME-сообщение и записать
	mimeBytes, err := buildMIMEMessage(msg)
	if err != nil {
		wc.Close()
		return fmt.Errorf("email: build MIME: %w", err)
	}
	if _, err := wc.Write(mimeBytes); err != nil {
		wc.Close()
		return fmt.Errorf("email: write message: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("email: close DATA writer: %w", err)
	}

	// 9. QUIT
	if err := client.Quit(); err != nil {
		// QUIT ошибка некритична — письмо уже отправлено
		d.logger.WarnContext(ctx, "SMTP QUIT error (non-fatal)", slog.String("error", err.Error()))
	}

	d.logger.DebugContext(ctx, "Email sent successfully",
		slog.String("from", from),
		slog.Any("to", msg.To),
	)
	return nil
}

// Health проверяет доступность SMTP-сервера через NOOP.
func (d *SMTPDialer) Health(ctx context.Context) error {
	addr := d.addr()
	conn, err := net.DialTimeout("tcp", addr, smtpDialTimeout)
	if err != nil {
		return fmt.Errorf("email health: dial %s: %w", addr, err)
	}

	client, err := smtp.NewClient(conn, d.config.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("email health: smtp.NewClient: %w", err)
	}
	defer client.Close()

	if err := client.Noop(); err != nil {
		return fmt.Errorf("email health: NOOP: %w", err)
	}
	return nil
}

// Close — no-op для stateless клиента.
func (d *SMTPDialer) Close() error {
	return nil
}

// buildMIMEMessage собирает MIME multipart/alternative сообщение (text/plain + text/html).
// Возвращает raw bytes готовые для записи в smtp.Data writer.
func buildMIMEMessage(msg *EmailMessage) ([]byte, error) {
	var buf bytes.Buffer

	// Заголовки письма
	from := msg.From
	if from == "" {
		from = "alertmanager@localhost"
	}

	buf.WriteString("From: " + from + "\r\n")
	buf.WriteString("To: " + strings.Join(msg.To, ", ") + "\r\n")
	buf.WriteString("Subject: " + mime64Subject(msg.Subject) + "\r\n")
	buf.WriteString("MIME-Version: 1.0\r\n")

	// Дополнительные заголовки
	for k, v := range msg.Headers {
		buf.WriteString(k + ": " + v + "\r\n")
	}

	// multipart/alternative writer
	mw := multipart.NewWriter(&buf)
	buf.WriteString("Content-Type: multipart/alternative; boundary=\"" + mw.Boundary() + "\"\r\n")
	buf.WriteString("\r\n")

	// text/plain часть
	if msg.Text != "" {
		partHeader := textproto.MIMEHeader{}
		partHeader.Set("Content-Type", "text/plain; charset=UTF-8")
		partHeader.Set("Content-Transfer-Encoding", "quoted-printable")

		pw, err := mw.CreatePart(partHeader)
		if err != nil {
			return nil, fmt.Errorf("create text part: %w", err)
		}
		qw := quotedprintable.NewWriter(pw)
		if _, err := qw.Write([]byte(msg.Text)); err != nil {
			return nil, fmt.Errorf("write text part: %w", err)
		}
		if err := qw.Close(); err != nil {
			return nil, fmt.Errorf("close text QP writer: %w", err)
		}
	}

	// text/html часть
	if msg.HTML != "" {
		partHeader := textproto.MIMEHeader{}
		partHeader.Set("Content-Type", "text/html; charset=UTF-8")
		partHeader.Set("Content-Transfer-Encoding", "quoted-printable")

		pw, err := mw.CreatePart(partHeader)
		if err != nil {
			return nil, fmt.Errorf("create html part: %w", err)
		}
		qw := quotedprintable.NewWriter(pw)
		if _, err := qw.Write([]byte(msg.HTML)); err != nil {
			return nil, fmt.Errorf("write html part: %w", err)
		}
		if err := qw.Close(); err != nil {
			return nil, fmt.Errorf("close html QP writer: %w", err)
		}
	}

	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("close multipart writer: %w", err)
	}

	return buf.Bytes(), nil
}

// mime64Subject кодирует тему письма в ASCII-safe форму.
// Если тема содержит только ASCII — возвращает как есть.
func mime64Subject(subject string) string {
	for _, r := range subject {
		if r > 127 {
			// Нужна кодировка — используем UTF-8 quoted-printable (RFC 2047)
			return "=?UTF-8?Q?" + encodeQP(subject) + "?="
		}
	}
	return subject
}

// encodeQP кодирует строку в RFC 2047 quoted-printable для заголовков.
func encodeQP(s string) string {
	var b strings.Builder
	for _, r := range []byte(s) {
		if r == ' ' {
			b.WriteByte('_')
		} else if r > 127 || r == '=' || r == '?' || r == '_' {
			b.WriteString(fmt.Sprintf("=%02X", r))
		} else {
			b.WriteByte(r)
		}
	}
	return b.String()
}
