// cmd/rpcserver/static/browser_automation_mock.js

// This is a MOCK implementation of browser automation functions
// for the example project. In a real scenario, this would interact
// with Puppeteer, Playwright, or similar.

window.BrowserAutomationMock = {
    logAction: function(action, params, result) {
        const logEntry = document.createElement('div');
        const timestamp = new Date().toLocaleTimeString();
        logEntry.innerHTML = `<strong>[${timestamp}] ${action}</strong>: Params: <code>${JSON.stringify(params)}</code> <br/>&nbsp;&nbsp;&nbsp;Result: <code>${JSON.stringify(result)}</code>`;
        document.getElementById('action-log').prepend(logEntry);
    },

    click: async function(params) {
        return new Promise(resolve => {
            const { selector } = params;
            const result = { success: true, message: `Mock clicked element: ${selector}` };
            this.logAction('browser.click', params, result);

            // Simulate UI change
            const statusEl = document.getElementById('click-status');
            if (statusEl) {
                statusEl.textContent = `Clicked ${selector} at ${new Date().toLocaleTimeString()}`;
                statusEl.style.color = 'green';
            }
            setTimeout(() => resolve(result), 200); // Simulate async operation
        });
    },

    input: async function(params) {
        return new Promise(resolve => {
            const { selector, text } = params;
            const result = { success: true, message: `Mock inputted '${text}' into element: ${selector}` };
            this.logAction('browser.input', params, result);

            // Simulate UI change
            const statusEl = document.getElementById('input-status');
            if (statusEl) {
                statusEl.textContent = `Inputted "${text}" into ${selector} at ${new Date().toLocaleTimeString()}`;
                statusEl.style.color = 'blue';
            }
            const inputTarget = document.querySelector(selector);
            if (inputTarget && typeof inputTarget.value !== 'undefined') {
                inputTarget.value = text;
            }

            setTimeout(() => resolve(result), 300);
        });
    },

    navigate: async function(params) {
        return new Promise(resolve => {
            const { url } = params;
            const result = { success: true, message: `Mock navigated to URL: ${url}`, newUrl: url };
            this.logAction('browser.navigate', params, result);
            
            const statusEl = document.getElementById('navigate-status');
            if (statusEl) {
                statusEl.textContent = `Navigated to ${url} at ${new Date().toLocaleTimeString()}`;
                statusEl.style.color = 'purple';
            }
            // In a real scenario, this would change window.location or iframe src
            setTimeout(() => resolve(result), 500);
        });
    },

    getText: async function(params) {
        return new Promise(resolve => {
            const { selector } = params;
            let textContent = `Mock text from ${selector}`;
            try {
                const el = document.querySelector(selector);
                if (el) {
                    textContent = el.textContent || el.innerText || el.value || `Mock text from ${selector}`;
                }
            } catch (e) { /* ignore for mock */ }
            
            const result = { success: true, text: textContent };
            this.logAction('browser.getText', params, result);
            setTimeout(() => resolve(result), 100);
        });
    },

    screenshot: async function(params) {
        return new Promise(resolve => {
            const result = { success: true, message: "Mock screenshot taken", imageDataBase64: "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII=" }; // 1x1 black pixel
            this.logAction('browser.screenshot', params, result);
             const statusEl = document.getElementById('screenshot-status');
            if (statusEl) {
                statusEl.innerHTML = `Screenshot taken at ${new Date().toLocaleTimeString()}. <img src="${result.imageDataBase64}" alt="mock screenshot" width="50"/>`;
                statusEl.style.color = 'orange';
            }
            setTimeout(() => resolve(result), 400);
        });
    }
};