package publishing

import (
	"errors"
	"net"
	"strings"
)

// ProviderEmail — идентификатор провайдера для метрик и ошибок.
const ProviderEmail = "email"

// smtpErrorCode извлекает числовой код из SMTP-ответа вида "535 ..." или "5.x.x ...".
// Возвращает 0 если код не найден.
func smtpErrorCode(msg string) int {
	if len(msg) < 3 {
		return 0
	}
	code := 0
	for i := 0; i < 3; i++ {
		c := msg[i]
		if c < '0' || c > '9' {
			return 0
		}
		code = code*10 + int(c-'0')
	}
	return code
}

// classifyEmailError классифицирует ошибку SMTP для метрик и retry-логики.
// Возвращает строку-категорию:
//   - "auth_error"         — SMTP 535 (authentication failed)
//   - "rate_limit"         — SMTP 421/451/452 (try again later)
//   - "invalid_recipient"  — SMTP 550/551/552/553 (bad recipient)
//   - "server_error"       — прочие 5xx
//   - "tls_error"          — TLS handshake / certificate
//   - "network_error"      — сетевые ошибки, timeout, dial fail
//   - "unknown"            — всё остальное
func classifyEmailError(err error) string {
	if err == nil {
		return "unknown"
	}

	msg := err.Error()

	// TLS errors — проверяем до network, т.к. могут оборачиваться
	if strings.Contains(msg, "tls:") ||
		strings.Contains(msg, "x509:") ||
		strings.Contains(msg, "certificate") ||
		strings.Contains(msg, "handshake") {
		return "tls_error"
	}

	// Network / timeout errors
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return "network_error"
		}
		return "network_error"
	}
	if strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "dial tcp") ||
		strings.Contains(msg, "i/o timeout") ||
		strings.Contains(msg, "EOF") {
		return "network_error"
	}

	// SMTP numeric codes
	code := smtpErrorCode(msg)
	switch code {
	case 535:
		return "auth_error"
	case 421, 451, 452:
		return "rate_limit"
	case 550, 551, 552, 553, 554:
		return "invalid_recipient"
	}
	if code >= 500 && code < 600 {
		return "server_error"
	}

	return "unknown"
}
