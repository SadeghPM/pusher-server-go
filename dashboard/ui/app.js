document.addEventListener('DOMContentLoaded', () => {
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

    let ws = null;
    let allApps = [];
    let isPaused = false;
    let events = [];

    // Check auth
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

    function renderOverview() {
        const appId = appSelector.value;
        const app = allApps.find(a => a.app_id === appId);
        if (!app) return;

        document.getElementById('overview-app-id').value = app.app_id;
        document.getElementById('overview-key').value = app.app_key || '';
        document.getElementById('overview-secret').value = app.app_secret || '';
        document.getElementById('overview-origins').value = (app.allowed_origins || []).join('\n');
        document.getElementById('overview-webhooks').value = (app.webhooks || []).join('\n');
    }

    navDebug.addEventListener('click', (e) => {
        e.preventDefault();
        navDebug.classList.add('active');
        navOverview.classList.remove('active');
        viewDebug.style.display = 'block';
        viewOverview.style.display = 'none';
        appTitle.textContent = 'Debug console';
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

    btnLogout.addEventListener('click', () => {
        localStorage.removeItem('pusher_admin_token');
        location.reload();
    });

    async function initDashboard() {
        modal.style.display = 'none';
        appContainer.style.display = 'block';

        try {
            const res = await fetch('/api/apps', {
                headers: { 'Authorization': `Bearer ${token}` }
            });
            if (res.status === 401) {
                throw new Error("Unauthorized");
            }
            const apps = await res.json();
            allApps = apps;

            appSelector.innerHTML = '';
            apps.forEach(app => {
                const opt = document.createElement('option');
                opt.value = app.app_id;
                opt.textContent = app.name;
                appSelector.appendChild(opt);
            });

            if (apps.length > 0) {
                connectWebSocket(apps[0].app_id);
            }

            appSelector.addEventListener('change', (e) => {
                connectWebSocket(e.target.value);
                renderOverview();
            });

        renderOverview();
        } catch (err) {
            alert('Authentication failed or server error.');
            localStorage.removeItem('pusher_admin_token');
            location.reload();
        }
    }

    function connectWebSocket(appId) {
        if (ws) {
            ws.close();
        }
        clearLogs();

        const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        const wsUrl = `${protocol}//${window.location.host}/ws?app_id=${appId}&token=${token}`;

        ws = new WebSocket(wsUrl);

        ws.onmessage = (event) => {
            if (isPaused) return;
            const data = JSON.parse(event.data);
            addEvent(data);
        };

        ws.onclose = () => {
            console.log("WS closed");
        };
    }

    function addEvent(ev) {
        events.unshift(ev); // add to top
        if (events.length > 100) events.pop();
        renderEvents();
    }

    function clearLogs() {
        events = [];
        renderEvents();
    }

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

            // Format time
            const date = new Date(e.time);
            const timeStr = date.toLocaleTimeString([], {hour12: false}) + '.' + date.getMilliseconds();

            // Event column
            let eventBadge = `<span class="badge">${e.type}</span>`;
            if (e.event) eventBadge = `<div><strong>${e.event}</strong></div><div style="margin-top:5px;">${eventBadge}</div>`;

            // Details column
            let detailsHtml = '<div class="event-details">';
            if (e.channel) detailsHtml += `<div class="detail-row"><span class="detail-label">Channel</span><span class="detail-value">${e.channel}</span></div>`;
            if (e.socket_id) detailsHtml += `<div class="detail-row"><span class="detail-label">Socket</span><span class="detail-value">${e.socket_id}</span></div>`;
            if (e.data) detailsHtml += `<div class="detail-row"><span class="detail-label">Data</span><span class="detail-value">${e.data}</span></div>`;
            detailsHtml += '</div>';

            tr.innerHTML = `
                <td>${eventBadge}</td>
                <td>${detailsHtml}</td>
                <td style="white-space: nowrap; color: #6c757d;">${timeStr}</td>
            `;
            eventLog.appendChild(tr);
        });
    }

    // UI Actions
    btnPause.addEventListener('click', () => {
        isPaused = !isPaused;
        btnPause.textContent = isPaused ? 'Resume' : 'Pause';
        btnPause.style.background = isPaused ? '#ffdfdf' : '';
    });

    btnClear.addEventListener('click', clearLogs);

    searchInput.addEventListener('input', renderEvents);

    toggleCreator.addEventListener('click', () => {
        const isHidden = creatorBody.style.display === 'none';
        creatorBody.style.display = isHidden ? 'block' : 'none';
        document.getElementById('toggle-icon').textContent = isHidden ? '▲' : '▼';
    });

    btnSendEvent.addEventListener('click', async () => {
        const appId = appSelector.value;
        const channel = document.getElementById('creator-channel').value;
        const eventName = document.getElementById('creator-event').value;
        let data = document.getElementById('creator-data').value;

        try {
            // validate json loosely
            JSON.parse(data);
        } catch(e) {
            // wrap in string if not valid json object
            data = `"${data}"`;
        }

        const payload = { app_id: appId, channel, event: eventName, data: JSON.parse(data) };

        btnSendEvent.disabled = true;
        btnSendEvent.textContent = 'Sending...';

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
            btnSendEvent.textContent = 'Send event';
        }
    });

});
