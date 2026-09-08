(() => {
  const list = document.querySelector('#bookList');
  const empty = document.querySelector('#empty');
  const tpl = document.querySelector('#bookTemplate');
  const notice = document.querySelector('#notice');
  const filter = document.querySelector('#filter');
  let data = null;

  const pct = value => Math.max(0, Math.min(100, Number(value) || 0));
  const when = value => {
    if (!value || value.startsWith('0001-')) return 'never synced';
    const d = new Date(value);
    return Number.isNaN(d.getTime()) ? 'unknown time' : d.toLocaleString();
  };
  const statusLabel = status => ({
    synced: 'In sync',
    conflict: 'Conflict',
    unmapped: 'Unmapped',
    error: 'Error',
    queued: 'Queued',
    'moon-ahead': 'Moon+ ahead',
    'remote-ahead': 'Backend ahead'
  }[status] || status);

  function flash(message) {
    notice.textContent = message;
    notice.classList.remove('hidden');
    clearTimeout(flash.timer);
    flash.timer = setTimeout(() => notice.classList.add('hidden'), 3200);
  }

  async function api(url, options = {}) {
    const headers = { ...(options.headers || {}) };
    if (options.body) headers['Content-Type'] = 'application/json';
    const res = await fetch(url, { ...options, headers, credentials: 'same-origin' });
    if (!res.ok) throw new Error((await res.text()) || res.statusText);
    return res.headers.get('content-type')?.includes('json') ? res.json() : null;
  }

  function render() {
    if (!data) return;
    const query = filter.value.trim().toLowerCase();
    list.replaceChildren();

    const books = data.books.filter(book =>
      !query ||
      book.key.toLowerCase().includes(query) ||
      (book.backend_id || '').toLowerCase().includes(query)
    );

    empty.classList.toggle('hidden', data.books.length !== 0);

    books.forEach(book => {
      const node = tpl.content.firstElementChild.cloneNode(true);
      node.dataset.key = book.key;
      node.querySelector('.book-title').textContent = book.key;
      node.querySelector('.book-meta').textContent = 'Moon+ updated ' + when(book.updated_at);

      const chip = node.querySelector('.status-chip');
      chip.textContent = statusLabel(book.status);
      chip.dataset.status = book.status;

      const moonPercent = pct(book.moon_percent);
      const backendPercent = pct(book.backend_percent);
      node.querySelector('.moon-value').textContent = moonPercent.toFixed(1) + '%';
      node.querySelector('.backend-value').textContent = backendPercent.toFixed(1) + '%';
      node.querySelector('.moon-bar').style.width = moonPercent + '%';
      node.querySelector('.backend-bar').style.width = backendPercent + '%';

      const mapInput = node.querySelector('.mapping-input');
      mapInput.value = book.backend_id || '';

      node.querySelector('.save-map').addEventListener('click', async () => {
        const backendID = mapInput.value.trim();
        if (!backendID) return flash('Enter a backend book ID first.');
        try {
          await api('/api/mappings', {
            method: 'POST',
            body: JSON.stringify({ book_key: book.key, backend_id: backendID })
          });
          flash('Mapping saved.');
          await load();
        } catch (err) {
          flash('Mapping failed: ' + err.message);
        }
      });

      const conflict = node.querySelector('.conflict-actions');
      conflict.classList.toggle('hidden', book.status !== 'conflict');

      node.querySelector('.keep-moon').addEventListener('click', async () => {
        if (!confirm('Push the current Moon+ progress to the backend for this book?')) return;
        try {
          await api('/api/conflicts', {
            method: 'POST',
            body: JSON.stringify({ book_key: book.key, action: 'keep-moon' })
          });
          flash('Moon+ position selected. Backend update queued.');
          await load();
        } catch (err) {
          flash('Conflict action failed: ' + err.message);
        }
      });

      node.querySelector('.ignore-remote').addEventListener('click', async () => {
        try {
          await api('/api/conflicts', {
            method: 'POST',
            body: JSON.stringify({ book_key: book.key, action: 'ignore-until-caught-up' })
          });
          flash('Backend warning hidden until Moon+ catches up.');
          await load();
        } catch (err) {
          flash('Conflict action failed: ' + err.message);
        }
      });

      const errorLine = node.querySelector('.error-line');
      if (book.error) {
        errorLine.textContent = book.error;
        errorLine.classList.remove('hidden');
      }

      list.appendChild(node);
    });
  }

  async function load() {
    try {
      data = await api('/api/dashboard');
      const health = data.backend_health?.state || 'unknown';
      document.querySelector('#backendBadge').textContent =
        data.backend === 'none' ? 'WebDAV only' : data.backend + ' · ' + health;
      document.querySelector('#statBooks').textContent = data.summary.books;
      document.querySelector('#statConflicts').textContent = data.summary.conflicts;
      document.querySelector('#statUnmapped').textContent = data.summary.unmapped;
      document.querySelector('#statErrors').textContent = data.summary.errors;
      render();
    } catch (err) {
      flash('Could not load dashboard: ' + err.message);
    }
  }

  filter.addEventListener('input', render);
  document.querySelector('#refresh').addEventListener('click', load);
  load();
  setInterval(load, 30000);
})();
