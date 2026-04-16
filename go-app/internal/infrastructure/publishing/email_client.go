package publishing

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"log/slog"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"sort"
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

// dialSMTP устанавливает соединение и создаёт SMTP client.
// Для DirectTLS: TLS-обёртка поверх TCP до создания SMTP client.
// Для STARTTLS: обычный TCP, STARTTLS вызывается позже в вызывающем коде.
func (d *SMTPDialer) dialSMTP(ctx context.Context) (*smtp.Client, error) {
	addr := d.addr()
	netDialer := &net.Dialer{Timeout: smtpDialTimeout}

	if d.config.DirectTLS {
		// Direct TLS (SMTPS, порт 465): TLS handshake до SMTP banner
		rawConn, err := netDialer.DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, fmt.Errorf("email: dial %s: %w", addr, err)
		}
		tlsCfg := &tls.Config{
			ServerName: d.config.Host,
			MinVersion: tls.VersionTLS12,
		}
		tlsConn := tls.Client(rawConn, tlsCfg)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			rawConn.Close()
			return nil, fmt.Errorf("email: TLS handshake: %w", err)
		}
		// Apply context deadline to TLS connection for SMTP commands (mirrors plaintext path)
		if deadline, ok := ctx.Deadline(); ok {
			_ = tlsConn.SetDeadline(deadline)
		}
		client, err := smtp.NewClient(tlsConn, d.config.Host)
		if err != nil {
			tlsConn.Close()
			return nil, fmt.Errorf("email: smtp.NewClient (direct TLS): %w", err)
		}
		return client, nil
	}

	// Plaintext TCP (с опциональным STARTTLS после)
	conn, err := netDialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("email: dial %s: %w", addr, err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	client, err := smtp.NewClient(conn, d.config.Host)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("email: smtp.NewClient: %w", err)
	}
	return client, nil
}

// setupSMTPSession настраивает SMTP-сессию: STARTTLS (если нужен) и AUTH.
// Вызывается из SendEmail и Health для устранения дублирования кода.
func (d *SMTPDialer) setupSMTPSession(client *smtp.Client) error {
	if d.config.RequireTLS && !d.config.DirectTLS {
		tlsCfg := &tls.Config{
			ServerName: d.config.Host,
			MinVersion: tls.VersionTLS12,
		}
		if err := client.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("StartTLS: %w", err)
		}
	}
	if d.config.Username != "" {
		auth := smtp.PlainAuth("", d.config.Username, d.config.Password, d.config.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("AUTH: %w", err)
		}
	}
	return nil
}

// SendEmail отправляет письмо через SMTP.
// Каждый вызов: dial → (STARTTLS|TLS) → AUTH → MAIL FROM → RCPT TO → DATA → QUIT.
func (d *SMTPDialer) SendEmail(ctx context.Context, msg *EmailMessage) error {
	// Фильтруем пустые адреса до начала сессии — защита от []string{""}
	validTo := make([]string, 0, len(msg.To))
	for _, addr := range msg.To {
		if trimmed := strings.TrimSpace(addr); trimmed != "" {
			validTo = append(validTo, trimmed)
		}
	}
	if len(validTo) == 0 {
		return fmt.Errorf("email: no recipients specified")
	}

	d.logger.DebugContext(ctx, "Connecting to SMTP server",
		slog.String("addr", d.addr()),
		slog.Int("recipients", len(validTo)),
	)

	client, err := d.dialSMTP(ctx)
	if err != nil {
		return err
	}
	// closed отслеживает, закрыто ли соединение через Quit().
	// Предотвращает двойное закрытие: Quit() уже закрывает conn,
	// поэтому defer должен вызывать Close() только при ошибках до Quit.
	var closed bool
	defer func() {
		if !closed {
			_ = client.Close()
		}
	}()

	if err := d.setupSMTPSession(client); err != nil {
		return fmt.Errorf("email: %w", err)
	}

	// MAIL FROM
	from := msg.From
	if from == "" {
		from = d.config.From
	}
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("email: MAIL FROM <%s>: %w", from, err)
	}

	// RCPT TO для каждого валидного получателя
	for _, to := range validTo {
		if err := client.Rcpt(to); err != nil {
			return fmt.Errorf("email: RCPT TO <%s>: %w", to, err)
		}
	}

	// DATA
	wc, err := client.Data()
	if err != nil {
		return fmt.Errorf("email: DATA: %w", err)
	}

	// Собрать MIME-сообщение
	mimeBytes, err := buildMIMEMessage(msg, from)
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

	// QUIT — отмечаем closed=true до вызова, чтобы defer не дублировал закрытие.
	// Quit() сам закрывает соединение (посылает команду QUIT и закрывает conn).
	closed = true
	if err := client.Quit(); err != nil {
		// QUIT ошибка некритична — письмо уже отправлено
		d.logger.WarnContext(ctx, "SMTP QUIT error (non-fatal)", slog.String("error", err.Error()))
	}

	d.logger.DebugContext(ctx, "Email sent successfully",
		slog.String("from", from),
		slog.Any("to", validTo),
	)
	return nil
}

// Health проверяет доступность SMTP-сервера через NOOP с полной аутентификацией.
// Выполняет те же шаги что и SendEmail (TLS + AUTH) для честного health check —
// без этого серверы с обязательным TLS вернут ложный OK на уровне TCP.
func (d *SMTPDialer) Health(ctx context.Context) error {
	client, err := d.dialSMTP(ctx)
	if err != nil {
		return fmt.Errorf("email health: %w", err)
	}
	defer client.Close()

	if err := d.setupSMTPSession(client); err != nil {
		return fmt.Errorf("email health: %w", err)
	}

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
// resolvedFrom — адрес отправителя уже разрешённый вызывающим кодом (согласован с SMTP MAIL FROM).
// Возвращает raw bytes готовые для записи в smtp.Data writer.
func buildMIMEMessage(msg *EmailMessage, resolvedFrom string) ([]byte, error) {
	var buf bytes.Buffer

	// Message-ID (RFC 2822 §3.6.4) — требуется большинством MTA для предотвращения spam-reject
	buf.WriteString("Message-ID: " + generateMessageID(resolvedFrom) + "\r\n")
	buf.WriteString("Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n")
	buf.WriteString("From: " + resolvedFrom + "\r\n")

	// Фильтрация пустых адресов для заголовка To (реальная доставка идёт через validTo в SendEmail)
	filteredTo := make([]string, 0, len(msg.To))
	for _, addr := range msg.To {
		if trimmed := strings.TrimSpace(addr); trimmed != "" {
			filteredTo = append(filteredTo, trimmed)
		}
	}
	buf.WriteString("To: " + strings.Join(filteredTo, ", ") + "\r\n")

	buf.WriteString("Subject: " + mime47Subject(msg.Subject) + "\r\n")
	buf.WriteString("MIME-Version: 1.0\r\n")

	// Дополнительные заголовки (sanitize CRLF для предотвращения header injection)
	// Сортировка ключей для детерминированного порядка заголовков в unit-тестах
	headerKeys := make([]string, 0, len(msg.Headers))
	for k := range msg.Headers {
		headerKeys = append(headerKeys, k)
	}
	sort.Strings(headerKeys)
	for _, k := range headerKeys {
		v := msg.Headers[k]
		buf.WriteString(sanitizeHeaderValue(k) + ": " + sanitizeHeaderValue(v) + "\r\n")
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

// mime47Subject кодирует тему письма согласно RFC 2047.
// Если тема ASCII-only — возвращает как есть.
// Для non-ASCII разбивает на encoded words (=?UTF-8?Q?...?=) длиной ≤75 символов.
func mime47Subject(subject string) string {
	for _, r := range subject {
		if r > 127 {
			return encodeRFC2047Words(subject)
		}
	}
	return subject
}

// encodeRFC2047Words кодирует строку в последовательность RFC 2047 encoded words.
// Каждый encoded word не превышает 75 символов (требование RFC 2047).
// Соседние words разделяются " " (folded whitespace) — MUA склеивает их обратно.
func encodeRFC2047Words(s string) string {
	// =?UTF-8?Q? = 10 символов, ?= = 2 символа → encoded text ≤ 63 символа
	const prefix = "=?UTF-8?Q?"
	const suffix = "?="
	const maxEncodedText = 63 // 75 - len(prefix) - len(suffix)

	encoded := encodeQP(s)
	if len(encoded) <= maxEncodedText {
		return prefix + encoded + suffix
	}

	var parts []string
	for len(encoded) > 0 {
		chunk := encoded
		if len(chunk) > maxEncodedText {
			chunk = encoded[:maxEncodedText]
			// Не разрывать посередине =XX escape-последовательности
			for len(chunk) > 0 {
				last := len(chunk) - 1
				if chunk[last] == '=' {
					chunk = chunk[:last]
				} else if last >= 1 && chunk[last-1] == '=' {
					chunk = chunk[:last-1]
				} else {
					break
				}
			}
		}
		parts = append(parts, prefix+chunk+suffix)
		encoded = encoded[len(chunk):]
	}
	return strings.Join(parts, " ")
}

// encodeQP кодирует строку в RFC 2047 quoted-printable для заголовков.
// Пробелы → '_', non-ASCII и специальные символы → =XX.
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

// sanitizeHeaderValue удаляет CR и LF из значений заголовков для предотвращения header injection.
func sanitizeHeaderValue(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	return strings.ReplaceAll(s, "\n", "")
}

// generateMessageID генерирует уникальный Message-ID заголовок для письма.
// Формат: <random-hex@domain> — соответствует RFC 2822 §3.6.4.
// Корректно обрабатывает RFC 5322 display-name format: "Name <addr@domain.com>".
func generateMessageID(from string) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	domain := "localhost"
	// net/mail.ParseAddress корректно разбирает оба формата:
	//   "user@domain.com" и "Name <user@domain.com>"
	// и возвращает только addr-spec без угловых скобок.
	if addr, err := mail.ParseAddress(from); err == nil {
		if idx := strings.LastIndex(addr.Address, "@"); idx >= 0 {
			domain = addr.Address[idx+1:]
		}
	} else if idx := strings.Index(from, "@"); idx >= 0 {
		// Fallback для bare-адресов без display-name
		domain = from[idx+1:]
	}
	return fmt.Sprintf("<%s@%s>", hex.EncodeToString(b), domain)
}
