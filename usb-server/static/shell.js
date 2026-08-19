(function () {
  const term = new Terminal({
    cursorBlink: true,
    fontSize: 14,
    theme: {
      background: '#11111b',
      foreground: '#cdd6f4'
    }
  });
  const fit = new FitAddon.FitAddon();
  term.loadAddon(fit);
  term.open(document.getElementById('terminal'));

  const proto = location.protocol === 'https:' ? 'wss://' : 'ws://';
  const ws = new WebSocket(proto + location.host + '/ws/shell');

  // Server sends binary PTY output; deliver as ArrayBuffer so we can write bytes
  ws.binaryType = 'arraybuffer';

  function sendResize() {
    if (ws.readyState !== WebSocket.OPEN) return;
    const dims = term.proposeDimensions();
    if (!dims) return;
    ws.send(JSON.stringify({ type: 'resize', cols: dims.cols, rows: dims.rows }));
  }

  ws.onopen = function () {
    fit.fit();
    sendResize();
    term.focus();
  };
  ws.onmessage = function (ev) {
    if (ev.data instanceof ArrayBuffer) {
      term.write(new Uint8Array(ev.data));
    } else if (typeof ev.data === 'string') {
      term.write(ev.data);
    }
  };
  ws.onclose = function () {
    term.write('\r\n\x1b[31m[connection closed]\x1b[0m\r\n');
  };
  ws.onerror = function () {
    term.write('\r\n\x1b[31m[websocket error]\x1b[0m\r\n');
  };

  // Keyboard input -> binary frame (control messages use text frames)
  term.onData(function (data) {
    if (ws.readyState !== WebSocket.OPEN) return;
    const buf = new Uint8Array(data.length);
    for (let i = 0; i < data.length; i++) buf[i] = data.charCodeAt(i);
    ws.send(buf);
  });

  term.onResize(function () {
    sendResize();
  });

  window.addEventListener('resize', function () {
    fit.fit();
    sendResize();
  });
})();
