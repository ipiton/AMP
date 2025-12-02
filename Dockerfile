FROM python:3.11-slim AS builder

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1

WORKDIR /app

# Системные зависимости для сборки бинарных wheels (только в builder-слое)
RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    && rm -rf /var/lib/apt/lists/*

# Устанавливаем зависимости в изолированный venv внутри builder-слоя
COPY requirements.txt ./
RUN python -m venv /opt/venv \
    && . /opt/venv/bin/activate \
    && pip install --upgrade pip \
    && pip install --no-cache-dir -r requirements.txt

FROM python:3.11-slim

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PATH="/opt/venv/bin:$PATH" \
    UVICORN_WORKERS=1 \
    LOG_LEVEL=info

WORKDIR /app

# Копируем только готовое окружение из builder-слоя
COPY --from=builder /opt/venv /opt/venv

# Копируем исходники приложения и шаблоны
COPY src ./src
COPY templates ./templates

# Безопасность: non-root пользователь
RUN useradd -r -u 10001 appuser && chown -R appuser:appuser /app
USER appuser

# Экспонируем порт сервиса
EXPOSE 8080

# Запуск приложения
CMD ["sh", "-c", "uvicorn src.alert_history.main:app --host 0.0.0.0 --port 8080 --proxy-headers --log-level ${LOG_LEVEL} --workers ${UVICORN_WORKERS}"]
