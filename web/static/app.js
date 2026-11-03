class ALRouter {
    constructor() {
        this.token = localStorage.getItem('al_router_token') || '';
        this.user = null;
        this.providers = [];
        this.models = [];
        this.init();
    }

    async init() {
        this.bindEvents();
        if (this.handleOAuthRedirect()) return;
        if (this.token) {
            await this.loadUser();
        } else {
            this.showAuthModal();
        }
    }

    handleOAuthRedirect() {
        const params = new URLSearchParams(window.location.search);
        const token = params.get('token');
        const error = params.get('error');
        if (!token && !error) return false;

        window.history.replaceState({}, '', window.location.pathname);

        if (token) {
            this.token = token;
            localStorage.setItem('al_router_token', token);
            this.loadUser();
            this.toast('Вы вошли через Google', 'success');
            return true;
        }

        const messages = {
            invalid_state: 'Сессия входа истекла, попробуйте снова',
            google_disabled: 'Вход через Google не настроен на сервере',
            exchange_failed: 'Google отклонил запрос',
            userinfo_failed: 'Не удалось получить данные профиля',
            no_email: 'Google не вернул email',
            access_denied: 'Вы отменили вход'
        };
        this.toast(messages[error] || 'Ошибка входа: ' + error, 'error');
        this.showAuthModal();
        return true;
    }

    bindEvents() {
        document.querySelectorAll('.nav-link').forEach(link => {
            link.addEventListener('click', (e) => {
                e.preventDefault();
                this.switchTab(link.dataset.tab);
            });
        });

        document.getElementById('logoutBtn').addEventListener('click', () => this.logout());
        document.getElementById('syncModelsBtn').addEventListener('click', () => this.syncModels());
        document.getElementById('copyMasterKeyBtn').addEventListener('click', () => this.copyMasterKey());
        document.getElementById('openOpenRouterBtn').addEventListener('click', () => window.open('https://openrouter.ai/settings/keys', '_blank'));
        document.getElementById('saveNvidiaKeyBtn').addEventListener('click', () => this.saveNvidiaKey());
        document.getElementById('addCustomProviderBtn').addEventListener('click', () => this.addCustomProvider());
        document.getElementById('copyAllConfigBtn').addEventListener('click', () => this.copyAllOpenRouterConfig());
        document.getElementById('deleteAccountBtn').addEventListener('click', () => this.confirmDeleteAccount());
        document.getElementById('modelSearch').addEventListener('input', () => this.filterModels());
        document.getElementById('modelProviderFilter').addEventListener('change', () => this.filterModels());

        document.getElementById('modalClose').addEventListener('click', () => this.hideModal());
        document.getElementById('modal').addEventListener('click', (e) => {
            if (e.target === e.currentTarget) this.hideModal();
        });
    }

    async api(path, options = {}) {
        const headers = {
            'Content-Type': 'application/json',
            ...options.headers
        };
        if (this.token) {
            headers['Authorization'] = `Bearer ${this.token}`;
        }

        const res = await fetch(path, {
            ...options,
            headers
        });

        if (res.status === 401) {
            this.logout();
            throw new Error('Unauthorized');
        }

        const data = await res.json().catch(() => ({}));
        if (!res.ok) {
            throw new Error(data.error || `HTTP ${res.status}`);
        }
        return data;
    }

    async loadUser() {
        try {
            this.user = await this.api('/api/me');
            this.renderUser();
            await Promise.all([
                this.loadProviders(),
                this.loadModels(),
                this.loadOpenRouterConfig()
            ]);
            this.updateStats();
        } catch (e) {
            this.toast('Ошибка загрузки: ' + e.message, 'error');
            this.logout();
        }
    }

    renderUser() {
        document.getElementById('userEmail').textContent = this.user.email;
        document.getElementById('settingsEmail').textContent = this.user.email;
        document.getElementById('settingsMasterKey').textContent = this.user.master_key;
        document.getElementById('nvidiaKeyInput').value = this.user.nvidia_key || '';
        document.getElementById('nvidiaKeyStatus').textContent = this.user.nvidia_key ? '✅ Ключ сохранён' : '❌ Ключ не задан';
    }

    async loadProviders() {
        const data = await this.api('/api/providers');
        this.providers = data.providers;
        this.renderProviders();
        this.updateProviderFilter();
    }

    renderProviders() {
        const grid = document.getElementById('providersGrid');
        grid.innerHTML = '';

        this.providers.forEach(p => {
            const card = document.createElement('div');
            card.className = `provider-card ${p.has_key ? 'connected' : ''}`;
            card.innerHTML = `
                <div class="provider-header">
                    <span class="provider-name">${this.escapeHtml(p.name)}</span>
                    <span class="provider-status ${p.has_key ? 'connected' : 'disconnected'}">
                        <span class="status-dot"></span>
                        ${p.has_key ? 'Подключено' : 'Не подключено'}
                    </span>
                </div>
                <div class="provider-base">${this.escapeHtml(p.api_base)}</div>
                <div class="provider-actions">
                    <button class="btn btn-primary ${p.has_key ? '' : 'btn-secondary'}" 
                        onclick="app.editProviderKey('${p.name}')">
                        ${p.has_key ? 'Изменить ключ' : 'Добавить ключ'}
                    </button>
                    ${p.has_key ? `<button class="btn btn-danger" onclick="app.deleteProviderKey('${p.name}')">Удалить</button>` : ''}
                </div>
            `;
            grid.appendChild(card);
        });
    }

    updateProviderFilter() {
        const select = document.getElementById('modelProviderFilter');
        const current = select.value;
        select.innerHTML = '<option value="">Все провайдеры</option>';
        [...new Set(this.providers.map(p => p.name))].forEach(name => {
            const opt = document.createElement('option');
            opt.value = name;
            opt.textContent = name;
            select.appendChild(opt);
        });
        select.value = current;
    }

    async loadModels() {
        const data = await this.api('/api/models');
        this.models = data.models;
        this.renderModels();
    }

    renderModels() {
        const grid = document.getElementById('modelsGrid');
        grid.innerHTML = '';

        const search = document.getElementById('modelSearch').value.toLowerCase();
        const providerFilter = document.getElementById('modelProviderFilter').value;

        const filtered = this.models.filter(m => {
            const matchSearch = m.id.toLowerCase().includes(search) ||
                (m.provider && m.provider.toLowerCase().includes(search));
            const matchProvider = !providerFilter || m.provider === providerFilter;
            return matchSearch && matchProvider;
        });

        if (filtered.length === 0) {
            grid.innerHTML = '<div class="empty-state">Модели не найдены</div>';
            return;
        }

        filtered.forEach(m => {
            const card = document.createElement('div');
            card.className = 'model-card';
            const caps = m.capabilities || {};
            const capTags = [];
            if (caps.chat_completion) capTags.push('💬 Chat');
            if (caps.vision) capTags.push('👁 Vision');
            if (caps.function_calling) capTags.push('⚡ Functions');
            if (caps.embedding) capTags.push('📊 Embeddings');
            if (caps.image_generation) capTags.push('🎨 Images');

            card.innerHTML = `
                <div class="model-header">
                    <span class="model-name">${this.escapeHtml(m.id)}</span>
                    <span class="model-provider">${this.escapeHtml(m.provider || 'unknown')}</span>
                </div>
                <div class="model-meta">
                    ${m.context_window ? `<span>📏 ${this.formatNumber(m.context_window)} ctx</span>` : ''}
                    ${m.max_output_tokens ? `<span>📤 ${this.formatNumber(m.max_output_tokens)} out</span>` : ''}
                    ${capTags.length ? `<span>${capTags.join(' · ')}</span>` : ''}
                </div>
            `;
            grid.appendChild(card);
        });
    }

    filterModels() {
        this.renderModels();
    }

    async loadOpenRouterConfig() {
        const data = await this.api('/api/openrouter-config');
        this.renderOpenRouterConfig(data);
    }

    renderOpenRouterConfig(config) {
        const container = document.getElementById('openrouterFields');
        container.innerHTML = '';

        config.fields.forEach(f => {
            const div = document.createElement('div');
            div.className = 'config-field';
            div.innerHTML = `
                <label>${this.escapeHtml(f.name)}</label>
                <div class="field-value">
                    <code id="field-${f.name.replace(/\s+/g, '-').toLowerCase()}">${this.escapeHtml(f.value)}</code>
                    <button class="btn btn-secondary" onclick="app.copyField('${f.name.replace(/\s+/g, '-').toLowerCase()}')">Копировать</button>
                </div>
                <small style="color: var(--text-muted); display: block; margin-top: 4px;">${this.escapeHtml(f.description)}</small>
            `;
            container.appendChild(div);
        });
    }

    async syncModels() {
        const btn = document.getElementById('syncModelsBtn');
        btn.disabled = true;
        btn.textContent = '⏳ Синхронизация...';
        try {
            await this.api('/api/sync', { method: 'POST' });
            await this.loadModels();
            this.toast('Модели синхронизированы', 'success');
        } catch (e) {
            this.toast('Ошибка: ' + e.message, 'error');
        } finally {
            btn.disabled = false;
            btn.textContent = '🔄 Синхронизировать модели';
        }
    }

    async editProviderKey(name) {
        const provider = this.providers.find(p => p.name === name);
        const key = prompt(`Введите API ключ для ${name}:`);
        if (!key) return;

        try {
            await this.api(`/api/providers/${name}/key`, {
                method: 'POST',
                body: JSON.stringify({ api_key: key })
            });
            this.toast(`Ключ для ${name} сохранён`, 'success');
            await this.loadProviders();
            await this.loadModels();
        } catch (e) {
            this.toast('Ошибка: ' + e.message, 'error');
        }
    }

    async deleteProviderKey(name) {
        if (!confirm(`Удалить ключ для ${name}?`)) return;
        try {
            await this.api(`/api/providers/${name}/key`, { method: 'DELETE' });
            this.toast(`Ключ для ${name} удалён`, 'success');
            await this.loadProviders();
            await this.loadModels();
        } catch (e) {
            this.toast('Ошибка: ' + e.message, 'error');
        }
    }

    async addCustomProvider() {
        const name = document.getElementById('customProviderName').value.trim();
        const base = document.getElementById('customProviderBase').value.trim();
        const key = document.getElementById('customProviderKey').value.trim();

        if (!name || !base) {
            this.toast('Заполните название и Base URL', 'error');
            return;
        }

        this.toast('Кастомные провайдеры требуют перезапуска сервера (добавьте в config.yaml)', 'info');
    }

    async saveNvidiaKey() {
        const key = document.getElementById('nvidiaKeyInput').value.trim();
        if (!key) {
            this.toast('Введите ключ', 'error');
            return;
        }

        try {
            await this.api('/api/nvidia-key', {
                method: 'POST',
                body: JSON.stringify({ api_key: key })
            });
            this.toast('NVIDIA ключ сохранён', 'success');
            document.getElementById('nvidiaKeyStatus').textContent = '✅ Ключ сохранён';
            await this.loadModels();
        } catch (e) {
            this.toast('Ошибка: ' + e.message, 'error');
        }
    }

    copyMasterKey() {
        navigator.clipboard.writeText(this.user.master_key);
        this.toast('Мастер-ключ скопирован', 'success');
    }

    copyField(fieldId) {
        const el = document.getElementById(`field-${fieldId}`);
        if (el) {
            navigator.clipboard.writeText(el.textContent);
            this.toast('Скопировано', 'success');
        }
    }

    copyAllOpenRouterConfig() {
        const fields = document.querySelectorAll('#openrouterFields code');
        let text = '';
        fields.forEach(el => {
            const label = el.closest('.config-field').querySelector('label').textContent;
            text += `${label}: ${el.textContent}\n`;
        });
        navigator.clipboard.writeText(text);
        this.toast('Вся конфигурация скопирована', 'success');
    }

    confirmDeleteAccount() {
        if (confirm('Точно удалить аккаунт? Это необратимо.')) {
            this.toast('Удаление аккаунта не реализовано в MVP', 'info');
        }
    }

    switchTab(tab) {
        document.querySelectorAll('.tab-content').forEach(t => t.classList.remove('active'));
        document.querySelectorAll('.nav-link').forEach(l => l.classList.remove('active'));
        document.getElementById(`tab-${tab}`).classList.add('active');
        document.querySelector(`[data-tab="${tab}"]`).classList.add('active');
    }

    showAuthModal() {
        this.showModal('Вход / Регистрация', `
            <div class="auth-form">
                <button class="btn btn-google" id="googleLoginBtn" type="button">
                    <svg width="18" height="18" viewBox="0 0 48 48" aria-hidden="true">
                        <path fill="#FFC107" d="M43.6 20.1H42V20H24v8h11.3C33.7 32.7 29.3 36 24 36c-6.6 0-12-5.4-12-12s5.4-12 12-12c3.1 0 5.9 1.2 8 3.1l5.7-5.7C34.3 7.1 29.4 5 24 5 12.9 5 4 13.9 4 25s8.9 20 20 20 20-8.9 20-20c0-1.3-.1-2.6-.4-4.9z"/>
                        <path fill="#FF3D00" d="M6.3 14.7l6.6 4.8C14.7 15.1 19 12 24 12c3.1 0 5.9 1.2 8 3.1l5.7-5.7C34.3 6.1 29.4 4 24 4 16.3 4 9.7 8.3 6.3 14.7z"/>
                        <path fill="#4CAF50" d="M24 44c5.2 0 9.9-2 13.4-5.2l-6.2-5.2C29.2 35.1 26.7 36 24 36c-5.2 0-9.6-3.3-11.3-7.9l-6.5 5C9.5 39.6 16.2 44 24 44z"/>
                        <path fill="#1976D2" d="M43.6 20.1H42V20H24v8h11.3c-.8 2.2-2.2 4.1-4.1 5.6l6.2 5.2C37 39.2 44 34 44 24c0-1.3-.1-2.6-.4-3.9z"/>
                    </svg>
                    Продолжить с Google
                </button>
                <div class="auth-divider"><span>или по email</span></div>
                <div class="form-group">
                    <label>Email</label>
                    <input type="email" id="authEmail" class="form-input" placeholder="you@example.com" required>
                </div>
                <div class="form-group">
                    <label>Пароль (мин. 8 символов)</label>
                    <input type="password" id="authPassword" class="form-input" placeholder="••••••••" required>
                </div>
                <p style="font-size: 0.8125rem; color: var(--text-muted); margin-top: 8px;">
                    Если аккаунта нет — он создастся автоматически
                </p>
            </div>
        `, `
            <button class="btn btn-primary" onclick="app.doAuth()">Войти / Зарегистрироваться</button>
        `);

        const googleBtn = document.getElementById('googleLoginBtn');
        googleBtn.addEventListener('click', () => { window.location.href = '/auth/google'; });

        fetch('/auth/google/enabled')
            .then(r => r.json())
            .then(d => {
                if (!d.enabled) {
                    googleBtn.disabled = true;
                    googleBtn.title = 'Google OAuth не настроен';
                    googleBtn.style.opacity = '0.5';
                }
            })
            .catch(() => {});
    }

    async doAuth() {
        const email = document.getElementById('authEmail').value.trim();
        const password = document.getElementById('authPassword').value;

        if (!email || !password) {
            this.toast('Заполните все поля', 'error');
            return;
        }

        try {
            let data;
            try {
                data = await this.api('/auth/login', {
                    method: 'POST',
                    body: JSON.stringify({ email, password })
                });
            } catch (e) {
                if (e.message.includes('Invalid credentials')) {
                    data = await this.api('/auth/register', {
                        method: 'POST',
                        body: JSON.stringify({ email, password })
                    });
                } else {
                    throw e;
                }
            }

            this.token = data.token;
            localStorage.setItem('al_router_token', this.token);
            this.hideModal();
            await this.loadUser();
            this.toast('Успешный вход!', 'success');
        } catch (e) {
            this.toast('Ошибка: ' + e.message, 'error');
        }
    }

    logout() {
        this.token = '';
        this.user = null;
        localStorage.removeItem('al_router_token');
        this.showAuthModal();
    }

    showModal(title, body, footer = '') {
        document.getElementById('modalTitle').textContent = title;
        document.getElementById('modalBody').innerHTML = body;
        document.getElementById('modalFooter').innerHTML = footer;
        document.getElementById('modal').classList.add('active');
    }

    hideModal() {
        document.getElementById('modal').classList.remove('active');
    }

    toast(message, type = 'info') {
        const existing = document.querySelector('.toast');
        if (existing) existing.remove();

        const toast = document.createElement('div');
        toast.className = `toast ${type}`;
        toast.textContent = message;
        document.body.appendChild(toast);

        setTimeout(() => {
            toast.style.animation = 'slideIn 0.3s ease reverse';
            setTimeout(() => toast.remove(), 300);
        }, 3000);
    }

    updateStats() {
        const keysCount = Object.keys(this.user?.provider_keys || {}).length + (this.user?.nvidia_key ? 1 : 0);
        document.getElementById('statKeys').textContent = keysCount;
        document.getElementById('statModels').textContent = this.models.length;
        document.getElementById('statProviders').textContent = this.providers.filter(p => p.has_key).length;
        document.getElementById('statMasterKey').textContent = this.user?.master_key ? '✅ Задан' : '❌ Нет';
    }

    escapeHtml(text) {
        const div = document.createElement('div');
        div.textContent = text;
        return div.innerHTML;
    }

    formatNumber(n) {
        if (n >= 1000000) return (n / 1000000).toFixed(1) + 'M';
        if (n >= 1000) return (n / 1000).toFixed(1) + 'K';
        return n.toString();
    }
}

document.addEventListener('DOMContentLoaded', () => {
    window.app = new ALRouter();
});