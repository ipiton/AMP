// TN-78: Real-time Updates Client (150% Quality Target)
// Provides SSE/WebSocket support for dashboard real-time updates

class RealtimeClient {
    constructor(options = {}) {
        this.options = {
            sseEndpoint: options.sseEndpoint || '/api/v2/events/stream',
            wsEndpoint: options.wsEndpoint || '/ws/dashboard',
            pollingInterval: options.pollingInterval || 30000, // 30s fallback
            reconnectDelay: options.reconnectDelay || 1000,
            maxReconnectDelay: options.maxReconnectDelay || 30000,
            ...options
        };

        this.eventBus = new EventTarget();
        this.connection = null;
        this.connectionType = null; // 'sse', 'websocket', 'polling'
        this.reconnectAttempts = 0;
        this.isConnected = false;
        this.pollInterval = null;
    }

    // Connect to real-time stream
    connect() {
        if (this.supportsSSE()) {
            this.connectSSE();
        } else if (this.supportsWebSocket()) {
            this.connectWebSocket();
        } else {
            this.fallbackPolling();
        }
    }

    // Check SSE support
    supportsSSE() {
        return typeof EventSource !== 'undefined';
    }

    // Check WebSocket support
    supportsWebSocket() {
        return typeof WebSocket !== 'undefined';
    }

    // Connect via SSE
    connectSSE() {
        try {
            this.connectionType = 'sse';
            const eventSource = new EventSource(this.options.sseEndpoint);

            eventSource.onopen = () => {
                this.isConnected = true;
                this.reconnectAttempts = 0;
                this.onConnect('sse');
            };

            eventSource.onmessage = (e) => {
                try {
                    const event = JSON.parse(e.data);
                    this.handleEvent(event);
                } catch (err) {
                    console.error('[RealtimeClient] Failed to parse SSE event:', err);
                }
            };

            eventSource.onerror = (err) => {
                console.error('[RealtimeClient] SSE error:', err);
                this.isConnected = false;
                eventSource.close();
                this.scheduleReconnect();
            };

            this.connection = eventSource;
        } catch (err) {
            console.error('[RealtimeClient] Failed to connect SSE:', err);
            this.fallbackToWebSocket();
        }
    }

    // Connect via WebSocket
    connectWebSocket() {
        try {
            this.connectionType = 'websocket';
            const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
            const wsUrl = `${protocol}//${window.location.host}${this.options.wsEndpoint}`;
            const ws = new WebSocket(wsUrl);

            ws.onopen = () => {
                this.isConnected = true;
                this.reconnectAttempts = 0;
                this.onConnect('websocket');
            };

            ws.onmessage = (e) => {
                try {
                    const event = JSON.parse(e.data);
                    this.handleEvent(event);
                } catch (err) {
                    console.error('[RealtimeClient] Failed to parse WebSocket event:', err);
                }
            };

            ws.onerror = (err) => {
                console.error('[RealtimeClient] WebSocket error:', err);
                this.isConnected = false;
            };

            ws.onclose = () => {
                this.isConnected = false;
                this.scheduleReconnect();
            };

            this.connection = ws;
        } catch (err) {
            console.error('[RealtimeClient] Failed to connect WebSocket:', err);
            this.fallbackPolling();
        }
    }

    // Fallback to polling
    fallbackPolling() {
        this.connectionType = 'polling';
        console.warn('[RealtimeClient] Real-time not available, using polling fallback');

        // Poll dashboard data every 30s
        this.pollInterval = setInterval(() => {
            this.fetchDashboardData();
        }, this.options.pollingInterval);
    }

    // Handle incoming event
    handleEvent(event) {
        // Emit custom event
        this.eventBus.dispatchEvent(new CustomEvent(event.type, { detail: event }));

        // Update dashboard based on event type
        switch (event.type) {
            case 'alert_created':
            case 'alert_resolved':
            case 'alert_firing':
            case 'alert_inhibited':
                this.updateAlertsSection(event);
                break;
            case 'stats_updated':
                this.updateStatsSection(event);
                break;
            case 'silence_created':
            case 'silence_updated':
            case 'silence_deleted':
            case 'silence_expired':
                this.updateSilencesSection(event);
                break;
            case 'health_changed':
                this.updateHealthSection(event);
                break;
        }

        // Show toast for critical events
        if (this.isCriticalEvent(event)) {
            this.showToast(event);
        }
    }

    // Update alerts section
    updateAlertsSection(event) {
        const alertsSection = document.querySelector('.alerts-section');
        if (!alertsSection) return;

        // Add visual indicator
        alertsSection.classList.add('updated');
        setTimeout(() => alertsSection.classList.remove('updated'), 2000);

        // Reload alerts (or update DOM directly)
        this.reloadAlertsSection();
    }

    // Update stats section
    updateStatsSection(event) {
        const stats = event.data;

        // Update stat cards
        this.updateStatCard('firing', stats.firing_alerts);
        this.updateStatCard('resolved', stats.resolved_today);
        this.updateStatCard('silences', stats.active_silences);
        this.updateStatCard('inhibited', stats.inhibited_alerts);
    }

    // Update stat card
    updateStatCard(type, value) {
        const card = document.querySelector(`.stat-card[data-type="${type}"]`);
        if (card) {
            const valueEl = card.querySelector('.stat-value');
            if (valueEl) {
                // Animate value change
                const oldValue = parseInt(valueEl.textContent) || 0;
                this.animateValue(valueEl, oldValue, value, 500);
            }
        }
    }

    // Animate value change
    animateValue(element, start, end, duration) {
        const startTime = performance.now();
        const change = end - start;

        const animate = (currentTime) => {
            const elapsed = currentTime - startTime;
            const progress = Math.min(elapsed / duration, 1);
            const current = Math.floor(start + change * progress);

            element.textContent = current;

            if (progress < 1) {
                requestAnimationFrame(animate);
            }
        };

        requestAnimationFrame(animate);
    }

    // Update silences section
    updateSilencesSection(event) {
        const silencesSection = document.querySelector('.silences-section');
        if (!silencesSection) return;

        // Add visual indicator
        silencesSection.classList.add('updated');
        setTimeout(() => silencesSection.classList.remove('updated'), 2000);

        // Reload silences (or update DOM directly)
        this.reloadSilencesSection();
    }

    // Update health section
    updateHealthSection(event) {
        const health = event.data;
        const componentEl = document.querySelector(`.health-component[data-component="${health.component}"]`);
        if (componentEl) {
            const statusEl = componentEl.querySelector('.health-status');
            if (statusEl) {
                statusEl.textContent = health.status;
                statusEl.className = `health-status status-${health.status}`;
            }
        }
    }

    // Reload alerts section (fetch from server)
    async reloadAlertsSection() {
        try {
            const response = await fetch('/api/v2/alerts?limit=10');
            if (response.ok) {
                const alerts = await response.json();
                this.renderAlertsSection(alerts);
            }
        } catch (err) {
            console.error('[RealtimeClient] Failed to reload alerts:', err);
        }
    }

    // Reload silences section (fetch from server)
    async reloadSilencesSection() {
        try {
            const response = await fetch('/api/v2/silences?limit=10');
            if (response.ok) {
                const silences = await response.json();
                this.renderSilencesSection(silences);
            }
        } catch (err) {
            console.error('[RealtimeClient] Failed to reload silences:', err);
        }
    }

    // Render alerts section (placeholder - implement based on your HTML structure)
    renderAlertsSection(alerts) {
        const alertsSection = document.querySelector('.alerts-section .alert-list');
        if (!alertsSection) return;

        // Update alerts list (simplified - implement full rendering)
        alertsSection.innerHTML = alerts.map(alert =>
            `<div class="alert-item">${alert.alertname} - ${alert.status}</div>`
        ).join('');
    }

    // Render silences section (placeholder - implement based on your HTML structure)
    renderSilencesSection(silences) {
        const silencesSection = document.querySelector('.silences-section .silence-list');
        if (!silencesSection) return;

        // Update silences list (simplified - implement full rendering)
        silencesSection.innerHTML = silences.map(silence =>
            `<div class="silence-item">${silence.id} - ${silence.status}</div>`
        ).join('');
    }

    // Schedule reconnect with exponential backoff
    scheduleReconnect() {
        if (this.reconnectAttempts >= 10) {
            console.error('[RealtimeClient] Max reconnect attempts reached, falling back to polling');
            this.fallbackPolling();
            return;
        }

        this.reconnectAttempts++;
        const delay = Math.min(
            this.options.reconnectDelay * Math.pow(2, this.reconnectAttempts - 1),
            this.options.maxReconnectDelay
        );

        console.log(`[RealtimeClient] Reconnecting in ${delay}ms (attempt ${this.reconnectAttempts})`);

        setTimeout(() => {
            this.connect();
        }, delay);
    }

    // Event listener registration
    on(eventType, callback) {
        this.eventBus.addEventListener(eventType, (e) => {
            callback(e.detail);
        });
    }

    // Disconnect
    disconnect() {
        if (this.connection) {
            if (this.connectionType === 'sse') {
                this.connection.close();
            } else if (this.connectionType === 'websocket') {
                this.connection.close();
            } else if (this.connectionType === 'polling') {
                clearInterval(this.pollInterval);
            }
        }
        this.isConnected = false;
    }

    // Check if event is critical
    isCriticalEvent(event) {
        const criticalTypes = ['alert_created', 'alert_firing', 'health_changed'];
        return criticalTypes.includes(event.type);
    }

    // Show toast notification
    showToast(event) {
        // Reuse toast function from dashboard.html if available
        if (typeof showToast === 'function') {
            const message = this.formatEventMessage(event);
            showToast(message, this.getEventSeverity(event));
        } else {
            // Fallback: console log
            console.log('[RealtimeClient] Event:', this.formatEventMessage(event));
        }
    }

    // Format event message
    formatEventMessage(event) {
        switch (event.type) {
            case 'alert_created':
                return `New alert: ${event.data.alertname}`;
            case 'alert_firing':
                return `Alert firing: ${event.data.alertname}`;
            case 'health_changed':
                return `Health changed: ${event.data.component} is ${event.data.status}`;
            default:
                return `Event: ${event.type}`;
        }
    }

    // Get event severity
    getEventSeverity(event) {
        if (event.type.startsWith('alert_')) {
            return event.data.severity === 'critical' ? 'error' : 'warning';
        }
        if (event.type === 'health_changed') {
            return event.data.status === 'unhealthy' ? 'error' : 'info';
        }
        return 'info';
    }

    // Fetch dashboard data (polling fallback)
    async fetchDashboardData() {
        try {
            const response = await fetch('/dashboard');
            if (response.ok) {
                // Parse HTML and update sections
                const html = await response.text();
                this.updateDashboardFromHTML(html);
            }
        } catch (err) {
            console.error('[RealtimeClient] Failed to fetch dashboard:', err);
        }
    }

    // Update dashboard from HTML (polling fallback)
    updateDashboardFromHTML(html) {
        const parser = new DOMParser();
        const doc = parser.parseFromString(html, 'text/html');

        // Update stats section
        const newStats = doc.querySelector('.stats-section');
        if (newStats) {
            const oldStats = document.querySelector('.stats-section');
            if (oldStats) {
                oldStats.innerHTML = newStats.innerHTML;
            }
        }

        // Update alerts section
        const newAlerts = doc.querySelector('.alerts-section');
        if (newAlerts) {
            const oldAlerts = document.querySelector('.alerts-section');
            if (oldAlerts) {
                oldAlerts.innerHTML = newAlerts.innerHTML;
            }
        }
    }

    // Callback when connection is established
    onConnect(connectionType) {
        console.log(`[RealtimeClient] Connected via ${connectionType}`);
    }

    // Fallback to WebSocket if SSE fails
    fallbackToWebSocket() {
        console.log('[RealtimeClient] SSE failed, trying WebSocket...');
        this.connectWebSocket();
    }
}

// Initialize RealtimeClient when dashboard loads
if (typeof window !== 'undefined') {
    document.addEventListener('DOMContentLoaded', function() {
        window.realtimeClient = new RealtimeClient({
            sseEndpoint: '/api/v2/events/stream',
            wsEndpoint: '/ws/dashboard',
        });

        window.realtimeClient.connect();

        // Listen for specific events (optional)
        window.realtimeClient.on('alert_created', (event) => {
            console.log('Alert created:', event);
        });

        window.realtimeClient.on('stats_updated', (event) => {
            console.log('Stats updated:', event);
        });
    });
}
