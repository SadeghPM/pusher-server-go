document.addEventListener('DOMContentLoaded', () => {
    // ==========================================
    // DOM References
    // ==========================================
    const modal = document.getElementById('token-modal');
    const appContainer = document.getElementById('app');
    const tokenInput = document.getElementById('admin-token');
    const btnSaveToken = document.getElementById('save-token');
    const btnLogout = document.getElementById('logout');

    const appSelector = document.getElementById('app-selector');
    const toggleCreator = document.getElementById('toggle-creator');
    const creatorBody = document.getElementById('creator-body');
    const btnSendEvent = document.getElementById('btn-send-event');

    const eventLog = document.getElementById('event-log');
    const emptyState = document.getElementById('empty-state');
    const btnPause = document.getElementById('btn-pause');
    const btnClear = document.getElementById('btn-clear');
    const searchInput = document.getElementById('search-events');
    const navDebug = document.getElementById('nav-debug');
    const navOverview = document.getElementById('nav-overview');
    const viewDebug = document.getElementById('view-debug');
    const viewOverview = document.getElementById('view-overview');
    const appTitle = document.getElementById('app-title');

    // Tenant modal elements
    const tenantModal = document.getElementById('tenant-modal');
    const btnAddTenant = document.getElementById('btn-add-tenant');
    const btnCloseTenantModal = document.getElementById('close-tenant-modal');
    const btnCancelTenant = document.getElementById('btn-cancel-tenant');
    const tenantForm = document.getElementById('tenant-form');
    const btnCreateTenant = document.getElementById('btn-create-tenant');
    const tenantError = document.getElementById('tenant-error');
    const tenantSuccess = document.getElementById('tenant-success');
    const tenantAppIdInput = document.getElementById('tenant-app-id');
    const slugError = document.getElementById('slug-error');

    // Config Overview elements
    const btnSaveConfig = document.getElementById('btn-save-config');
    const configSaveFeedback = document.getElementById('config-save-feedback');
    const configSaveError = document.getElementById('config-save-error');
    const overviewOrigins = document.getElementById('overview-origins');
    const overviewWebhooks = document.getElementById('overview-webhooks');

    // Delete modal elements
    const deleteModal = document.getElementById('delete-modal');
    const btnDeleteTenant = document.getElementById('btn-delete-tenant');
    const btnCancelDelete = document.getElementById('btn-cancel-delete');
    const btnConfirmDelete = document.getElementById('btn-confirm-delete');
    const deleteAppName = document.getElementById('delete-app-name');
    const deleteError = document.getElementById('delete-error');

    let ws = null;
    let allApps = [];
    let isPaused = false;
    let events = [];
    let appToDelete = null;

    // ==========================================
    // Auth
    // ==========================================
    let token = localStorage.getItem('pusher_admin_token');
    if (token) {
        initDashboard();
    } else {
        modal.style.display = 'flex';
    }

    btnSaveToken.addEventListener('click', () => {
        token = tokenInput.value.trim();
        if (token) {
            localStorage.setItem('pusher_admin_token', token);
            modal.style.display = 'none';
            initDashboard();
        }
    });

    // Allow Enter key to login
    tokenInput.addEventListener('keydown', (e) => {
        if (e.key === 'Enter') btnSaveToken.click();
    });

    btnLogout.addEventListener('click', () => {
        localStorage.removeItem('pusher_admin_token');
        location.reload();
    });

    // ==========================================
    // Init Dashboard
    // ==========================================
    async function initDashboard() {
        modal.style.display = 'none';
        appContainer.style.display = 'block';

        try {
            const res = await fetch('/api/apps', {
                headers: { 'Authorization': `Bearer ${token}` }
            });
            if (res.status === 401) throw new Error("Unauthorized");
            const apps = await res.json();
            allApps = apps;

            populateAppSelector(apps);
            if (apps.length > 0) {
                connectWebSocket(apps[0].app_id);
            }

            appSelector.addEventListener('change', (e) => {
                connectWebSocket(e.target.value);
                if (viewOverview.style.display !== 'none') renderOverview();
            });

            renderOverview();
        } catch (err) {
            alert('Authentication failed or server error.');
            localStorage.removeItem('pusher_admin_token');
            location.reload();
        }
    }

    function populateAppSelector(apps, selectAppId) {
        appSelector.innerHTML = '';
        apps.forEach(app => {
            const opt = document.createElement('option');
            opt.value = app.app_id;
            opt.textContent = app.name || app.app_id;
            appSelector.appendChild(opt);
        });
        if (selectAppId) {
            appSelector.value = selectAppId;
        }
    }

    // ==========================================
    // Navigation
    // ==========================================
    navDebug.addEventListener('click', (e) => {
        e.preventDefault();
        navDebug.classList.add('active');
        navOverview.classList.remove('active');
        viewDebug.style.display = 'block';
        viewOverview.style.display = 'none';
        appTitle.textContent = 'Debug Console';
    });

    navOverview.addEventListener('click', (e) => {
        e.preventDefault();
        navOverview.classList.add('active');
        navDebug.classList.remove('active');
        viewDebug.style.display = 'none';
        viewOverview.style.display = 'block';
        appTitle.textContent = 'Overview';
        renderOverview();
    });

    function renderOverview() {
        const appId = appSelector.value;
        const app = allApps.find(a => a.app_id === appId);
        if (!app) return;

        configSaveFeedback.style.display = 'none';
        configSaveError.style.display = 'none';

        document.getElementById('overview-app-id').value = app.app_id;
        document.getElementById('overview-key').value = app.app_key || '';
        document.getElementById('overview-secret').value = app.app_secret || '';
        overviewOrigins.value = (app.allowed_origins || []).join('\n');
        overviewWebhooks.value = (app.webhooks || []).join('\n');
    }

    btnSaveConfig.addEventListener('click', async () => {
        const appId = appSelector.value;
        if (!appId) return;

        configSaveFeedback.style.display = 'none';
        configSaveError.style.display = 'none';

        const originsRaw = overviewOrigins.value;
        const webhooksRaw = overviewWebhooks.value;

        const origins = originsRaw ? originsRaw.split('\n').map(s => s.trim()).filter(Boolean) : [];
        const webhooks = webhooksRaw ? webhooksRaw.split('\n').map(s => s.trim()).filter(Boolean) : [];

        btnSaveConfig.disabled = true;
        const originalText = btnSaveConfig.innerHTML;
        btnSaveConfig.innerHTML = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"></circle></svg> Saving...`;

        try {
            const res = await fetch('/api/apps', {
                method: 'PUT',
                headers: {
                    'Content-Type': 'application/json',
                    'Authorization': `Bearer ${token}`
                },
                body: JSON.stringify({
                    app_id: appId,
                    allowed_origins: origins,
                    webhooks: webhooks
                })
            });

            if (!res.ok) {
                const errText = await res.text();
                throw new Error(errText || 'Failed to save configuration');
            }

            const updatedApp = await res.json();
            
            // Update in the local array
            const index = allApps.findIndex(a => a.app_id === appId);
            if (index !== -1) {
                allApps[index] = updatedApp;
            }

            configSaveFeedback.textContent = 'Configuration saved successfully!';
            configSaveFeedback.style.display = 'block';
            setTimeout(() => {
                configSaveFeedback.style.display = 'none';
            }, 3000);
        } catch (err) {
            configSaveError.textContent = err.message;
            configSaveError.style.display = 'block';
        } finally {
            btnSaveConfig.disabled = false;
            btnSaveConfig.innerHTML = originalText;
        }
    });

    // ==========================================
    // WebSocket Connection
    // ==========================================
    function connectWebSocket(appId) {
        if (ws) ws.close();
        clearLogs();

        const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        const wsUrl = `${protocol}//${window.location.host}/ws?app_id=${appId}&token=${token}`;
        ws = new WebSocket(wsUrl);

        ws.onmessage = (event) => {
            if (isPaused) return;
            const data = JSON.parse(event.data);
            addEvent(data);
        };

        ws.onclose = () => console.log("WS closed");
    }

    function addEvent(ev) {
        events.unshift(ev);
        if (events.length > 200) events.pop();
        renderEvents();
    }

    function clearLogs() {
        events = [];
        renderEvents();
    }

    // ==========================================
    // Render Events
    // ==========================================
    const BADGE_CLASSES = {
        connection: 'badge-connection',
        disconnection: 'badge-disconnection',
        subscription: 'badge-subscription',
        api_message: 'badge-api_message',
        client_event: 'badge-client_event',
    };

    function renderEvents() {
        const filterText = searchInput.value.toLowerCase();
        const filtered = events.filter(e => {
            return e.type.toLowerCase().includes(filterText) ||
                   (e.channel && e.channel.toLowerCase().includes(filterText)) ||
                   (e.event && e.event.toLowerCase().includes(filterText));
        });

        if (filtered.length === 0) {
            eventLog.innerHTML = '';
            eventLog.appendChild(emptyState);
            emptyState.style.display = 'table-row';
            return;
        }

        eventLog.innerHTML = '';
        filtered.forEach(e => {
            const tr = document.createElement('tr');

            const date = new Date(e.time);
            const timeStr = date.toLocaleTimeString([], { hour12: false }) +
                            '.' + String(date.getMilliseconds()).padStart(3, '0');

            const badgeClass = BADGE_CLASSES[e.type] || 'badge-default';
            let eventCol = `<span class="badge ${badgeClass}">${e.type}</span>`;
            if (e.event) {
                eventCol = `<div><strong>${escapeHtml(e.event)}</strong></div><div style="margin-top:5px;">${eventCol}</div>`;
            }

            let detailsHtml = '<div class="event-details">';
            if (e.channel) detailsHtml += `<div class="detail-row"><span class="detail-label">Channel</span><span class="detail-value">${escapeHtml(e.channel)}</span></div>`;
            if (e.socket_id) detailsHtml += `<div class="detail-row"><span class="detail-label">Socket</span><span class="detail-value">${escapeHtml(e.socket_id)}</span></div>`;
            if (e.data) detailsHtml += `<div class="detail-row"><span class="detail-label">Data</span><span class="detail-value">${escapeHtml(e.data)}</span></div>`;
            detailsHtml += '</div>';

            tr.innerHTML = `
                <td>${eventCol}</td>
                <td>${detailsHtml}</td>
                <td style="white-space: nowrap; color: var(--text-muted); font-size: 12px; font-variant-numeric: tabular-nums;">${timeStr}</td>
            `;
            eventLog.appendChild(tr);
        });
    }

    function escapeHtml(str) {
        const div = document.createElement('div');
        div.textContent = str;
        return div.innerHTML;
    }

    // ==========================================
    // Controls
    // ==========================================
    btnPause.addEventListener('click', () => {
        isPaused = !isPaused;
        document.getElementById('pause-text').textContent = isPaused ? 'Resume' : 'Pause';
        const pauseIcon = document.getElementById('pause-icon');
        if (isPaused) {
            pauseIcon.innerHTML = '<polygon points="5 3 19 12 5 21 5 3"></polygon>';
            btnPause.classList.add('paused');
        } else {
            pauseIcon.innerHTML = '<rect x="6" y="4" width="4" height="16"></rect><rect x="14" y="4" width="4" height="16"></rect>';
            btnPause.classList.remove('paused');
        }
    });

    btnClear.addEventListener('click', clearLogs);
    searchInput.addEventListener('input', renderEvents);

    toggleCreator.addEventListener('click', () => {
        const isHidden = creatorBody.style.display === 'none';
        creatorBody.style.display = isHidden ? 'block' : 'none';
        document.getElementById('toggle-icon').classList.toggle('open', isHidden);
    });

    btnSendEvent.addEventListener('click', async () => {
        const appId = appSelector.value;
        const channel = document.getElementById('creator-channel').value;
        const eventName = document.getElementById('creator-event').value;
        let data = document.getElementById('creator-data').value;

        try {
            JSON.parse(data);
        } catch(e) {
            data = `"${data}"`;
        }

        const payload = { app_id: appId, channel, event: eventName, data: JSON.parse(data) };

        btnSendEvent.disabled = true;
        btnSendEvent.innerHTML = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"></circle></svg> Sending...`;

        try {
            const res = await fetch('/api/trigger', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                    'Authorization': `Bearer ${token}`
                },
                body: JSON.stringify(payload)
            });
            if (!res.ok) throw new Error('Failed to trigger');
        } catch(err) {
            alert('Failed to send event: ' + err.message);
        } finally {
            btnSendEvent.disabled = false;
            btnSendEvent.innerHTML = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="22" y1="2" x2="11" y2="13"></line><polygon points="22 2 15 22 11 13 2 9 22 2"></polygon></svg> Send Event`;
        }
    });

    // ==========================================
    // New Tenant Modal
    // ==========================================
    function openTenantModal() {
        tenantModal.style.display = 'flex';
        tenantError.style.display = 'none';
        tenantSuccess.style.display = 'none';
        tenantForm.reset();
        setTimeout(() => document.getElementById('tenant-app-id').focus(), 100);
    }

    function closeTenantModal() {
        tenantModal.style.display = 'none';
        tenantForm.reset();
        tenantError.style.display = 'none';
        tenantSuccess.style.display = 'none';
    }

    btnAddTenant.addEventListener('click', openTenantModal);
    btnCloseTenantModal.addEventListener('click', closeTenantModal);
    btnCancelTenant.addEventListener('click', closeTenantModal);

    tenantModal.addEventListener('click', (e) => {
        if (e.target === tenantModal) closeTenantModal();
    });

    tenantAppIdInput.addEventListener('input', () => {
        tenantError.style.display = 'none';
        slugError.style.display = 'none';
        
        const val = tenantAppIdInput.value.trim();
        if (!val) return;

        const slugPattern = /^[a-z0-9][a-z0-9-]{0,62}[a-z0-9]$/;
        if (val.length < 2 || val.length > 64 || !slugPattern.test(val)) {
            slugError.textContent = 'Invalid slug format (lowercase letters, numbers, and hyphens only; no leading/trailing hyphens; 2-64 chars).';
            slugError.style.display = 'block';
            return;
        }

        const isDuplicate = allApps.some(app => app.app_id.toLowerCase() === val.toLowerCase());
        if (isDuplicate) {
            slugError.textContent = 'App ID already exists. Must be unique.';
            slugError.style.display = 'block';
        }
    });

    tenantForm.addEventListener('submit', async (e) => {
        e.preventDefault();
        tenantError.style.display = 'none';
        tenantSuccess.style.display = 'none';
        slugError.style.display = 'none';

        const appId = tenantAppIdInput.value.trim();

        if (!appId) {
            tenantError.textContent = 'App ID is required.';
            tenantError.style.display = 'block';
            return;
        }

        const slugPattern = /^[a-z0-9][a-z0-9-]{0,62}[a-z0-9]$/;
        if (appId.length < 2 || appId.length > 64 || !slugPattern.test(appId)) {
            tenantError.textContent = 'App ID must be a valid slug (2-64 characters, lowercase letters, digits, and hyphens; no leading/trailing hyphens).';
            tenantError.style.display = 'block';
            return;
        }

        const isDuplicate = allApps.some(app => app.app_id.toLowerCase() === appId.toLowerCase());
        if (isDuplicate) {
            tenantError.textContent = `A tenant with App ID "${appId}" already exists.`;
            tenantError.style.display = 'block';
            return;
        }

        const payload = {
            app_id: appId
        };

        btnCreateTenant.disabled = true;
        btnCreateTenant.textContent = 'Creating...';

        try {
            const res = await fetch('/api/apps', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                    'Authorization': `Bearer ${token}`
                },
                body: JSON.stringify(payload)
            });

            if (!res.ok) {
                const errText = await res.text();
                throw new Error(errText || 'Failed to create tenant');
            }

            const newApp = await res.json();
            allApps.push(newApp);
            populateAppSelector(allApps, newApp.app_id);
            connectWebSocket(newApp.app_id);
            renderOverview();

            tenantSuccess.textContent = `Tenant "${newApp.app_id}" created successfully!`;
            tenantSuccess.style.display = 'block';
            tenantForm.reset();

            setTimeout(() => closeTenantModal(), 1200);
        } catch (err) {
            tenantError.textContent = err.message;
            tenantError.style.display = 'block';
        } finally {
            btnCreateTenant.disabled = false;
            btnCreateTenant.textContent = 'Create Tenant';
        }
    });

    // ==========================================
    // Delete Tenant Modal
    // ==========================================
    function openDeleteModal() {
        const appId = appSelector.value;
        if (!appId) return;
        appToDelete = appId;
        deleteAppName.textContent = appId;
        deleteError.style.display = 'none';
        deleteModal.style.display = 'flex';
    }

    function closeDeleteModal() {
        deleteModal.style.display = 'none';
        appToDelete = null;
        deleteError.style.display = 'none';
    }

    btnDeleteTenant.addEventListener('click', openDeleteModal);
    btnCancelDelete.addEventListener('click', closeDeleteModal);

    deleteModal.addEventListener('click', (e) => {
        if (e.target === deleteModal) closeDeleteModal();
    });

    btnConfirmDelete.addEventListener('click', async () => {
        if (!appToDelete) return;

        btnConfirmDelete.disabled = true;
        btnConfirmDelete.textContent = 'Deleting...';
        deleteError.style.display = 'none';

        try {
            const res = await fetch('/api/apps', {
                method: 'DELETE',
                headers: {
                    'Content-Type': 'application/json',
                    'Authorization': `Bearer ${token}`
                },
                body: JSON.stringify({ app_id: appToDelete })
            });

            if (!res.ok) {
                const errText = await res.text();
                throw new Error(errText || 'Failed to delete tenant');
            }

            // Remove from local list
            allApps = allApps.filter(a => a.app_id !== appToDelete);
            populateAppSelector(allApps);

            closeDeleteModal();

            // Switch to the first remaining app
            if (allApps.length > 0) {
                appSelector.value = allApps[0].app_id;
                connectWebSocket(allApps[0].app_id);
                renderOverview();
            } else {
                // No apps left
                if (ws) ws.close();
                clearLogs();
            }

            // Navigate back to debug view
            navDebug.click();
        } catch (err) {
            deleteError.textContent = err.message;
            deleteError.style.display = 'block';
        } finally {
            btnConfirmDelete.disabled = false;
            btnConfirmDelete.textContent = 'Delete Tenant';
        }
    });

    // ==========================================
    // Global Escape key handler
    // ==========================================
    document.addEventListener('keydown', (e) => {
        if (e.key === 'Escape') {
            if (tenantModal.style.display === 'flex') closeTenantModal();
            if (deleteModal.style.display === 'flex') closeDeleteModal();
        }
    });
});
