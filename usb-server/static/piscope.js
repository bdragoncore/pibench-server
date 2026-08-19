(function() {
  function initPiScope() {
    const canvas = document.getElementById('scope-canvas');
    if (!canvas) return;
    const ctx = canvas.getContext('2d');

    const btnToggleRun = document.getElementById('btn-toggle-run');
    const btnClearScope = document.getElementById('btn-clear-scope');
    const btnZoomIn = document.getElementById('btn-zoom-in');
    const btnZoomOut = document.getElementById('btn-zoom-out');
    const btnResetView = document.getElementById('btn-reset-view');
    const btnTestGen = document.getElementById('btn-test-gen');
    const btnExportVcd = document.getElementById('btn-export-vcd');
    const btnExportCsv = document.getElementById('btn-export-csv');
    const timebaseSelect = document.getElementById('timebase-select');
    const rateSelect = document.getElementById('rate-select');
    const decoderSelect = document.getElementById('decoder-select');
    const channelPills = document.getElementById('channel-pills');
    const metricsGrid = document.getElementById('metrics-grid');
    const scopeStatus = document.getElementById('scope-status');
    const scopeCursorInfo = document.getElementById('scope-cursor-info');

    const cursorAVal = document.getElementById('cursor-a-val');
    const cursorBVal = document.getElementById('cursor-b-val');
    const deltaTVal = document.getElementById('delta-t-val');
    const deltaFVal = document.getElementById('delta-f-val');

    let isRunning = true;
    let isTestGen = true;
    let timebaseMsPerDiv = 100;
    let sampleIntervalMs = 10;
    let mouseX = -1;
    let mouseY = -1;

    let cursorA_Ms = null;
    let cursorB_Ms = null;

    let m5adcVoltage = 1.65;
    let m5adcConnected = false;

    function fetchM5ADC() {
      fetch('/api/m5stickc/adc')
        .then(r => r.json())
        .then(d => {
          const badge = document.getElementById('m5stickc-status');
          if (d && d.connected) {
            m5adcConnected = true;
            m5adcVoltage = d.voltage_v || (d.voltage_mv / 1000.0) || 0;
            if (badge) {
              badge.textContent = `⚡ M5StickC G36 ADC: ${m5adcVoltage.toFixed(3)} V`;
              badge.style.background = 'rgba(255, 215, 0, 0.18)';
              badge.style.color = '#ffd700';
              badge.style.borderColor = 'rgba(255, 215, 0, 0.4)';
            }
          } else if (badge) {
            badge.textContent = `⚡ M5StickC G36 ADC (Disconnected)`;
            badge.style.background = 'rgba(255, 255, 255, 0.05)';
            badge.style.color = '#8080a0';
          }
        })
        .catch(() => {});
    }
    setInterval(fetchM5ADC, 250);

    const activePins = [2, 3, 4, 14, 15, 17, 18, 22, 23, 24, 25, 27];
    const selectedPins = new Set([2, 3, 4, 14, 15, 17, 18, 27]);

    const pinColors = {
      2: '#4caf50',
      3: '#00bcd4',
      4: '#ab47bc',
      14: '#ff9800',
      15: '#ffc107',
      17: '#2196f3',
      18: '#e91e63',
      22: '#40c4ff',
      23: '#69f0ae',
      24: '#eeff41',
      25: '#ff6e40',
      27: '#7c8aff'
    };

    const pinLabels = {
      2: 'GPIO2 (SDA1)',
      3: 'GPIO3 (SCL1)',
      4: 'GPIO4 (1-Wire)',
      14: 'GPIO14 (TXD1)',
      15: 'GPIO15 (RXD1)',
      17: 'GPIO17',
      18: 'GPIO18 (PWM0)',
      22: 'GPIO22',
      23: 'GPIO23',
      24: 'GPIO24',
      25: 'GPIO25',
      27: 'GPIO27'
    };

    const maxSamples = 4000;
    let sampleBuffer = [];
    let edgeStats = {};

    activePins.forEach(function(pin) {
      edgeStats[pin] = { transitions: 0, lastState: -1, highCount: 0, totalCount: 0 };
    });

    if (channelPills) {
      channelPills.innerHTML = '';
      activePins.forEach(function(pin) {
        const pill = document.createElement('button');
        pill.type = 'button';
        pill.className = 'channel-pill ' + (selectedPins.has(pin) ? 'active' : '');
        pill.style.setProperty('--chip-color', pinColors[pin] || '#7c8aff');
        pill.innerHTML = '<span class="dot" style="background:' + pinColors[pin] + '"></span> ' + (pinLabels[pin] || ('GPIO' + pin));
        pill.addEventListener('click', function() {
          if (selectedPins.has(pin)) {
            if (selectedPins.size > 1) selectedPins.delete(pin);
          } else {
            selectedPins.add(pin);
          }
          pill.classList.toggle('active', selectedPins.has(pin));
          renderMetrics();
        });
        channelPills.appendChild(pill);
      });
    }

    function resizeCanvas() {
      const wrap = canvas.parentElement;
      canvas.width = wrap.clientWidth || 1200;
      canvas.height = Math.max(520, Math.min(680, window.innerHeight - 340));
    }
    window.addEventListener('resize', resizeCanvas);
    resizeCanvas();

    canvas.addEventListener('click', function(e) {
      const rect = canvas.getBoundingClientRect();
      const clickX = e.clientX - rect.left;
      const width = canvas.width;
      const leftMargin = 150;
      const rightMargin = 25;
      const plotWidth = width - leftMargin - rightMargin;
      const numDivs = 10;
      const totalWindowMs = timebaseMsPerDiv * numDivs;

      if (clickX >= leftMargin && clickX <= width - rightMargin) {
        const timeOffsetMs = ((clickX - leftMargin) / plotWidth) * totalWindowMs - totalWindowMs;
        if (e.shiftKey) {
          cursorB_Ms = timeOffsetMs;
        } else {
          cursorA_Ms = timeOffsetMs;
        }
        updateDeltaBar();
      }
    });

    canvas.addEventListener('wheel', function(e) {
      e.preventDefault();
      if (e.deltaY < 0) {
        if (timebaseMsPerDiv > 10) timebaseMsPerDiv = Math.max(10, Math.round(timebaseMsPerDiv / 1.5));
      } else {
        if (timebaseMsPerDiv < 2000) timebaseMsPerDiv = Math.min(2000, Math.round(timebaseMsPerDiv * 1.5));
      }
      if (timebaseSelect) timebaseSelect.value = timebaseMsPerDiv;
    });

    canvas.addEventListener('mousemove', function(e) {
      const rect = canvas.getBoundingClientRect();
      mouseX = e.clientX - rect.left;
      mouseY = e.clientY - rect.top;
    });

    canvas.addEventListener('mouseleave', function() {
      mouseX = -1;
      mouseY = -1;
      if (scopeCursorInfo) scopeCursorInfo.style.display = 'none';
    });

    function updateDeltaBar() {
      if (cursorAVal) cursorAVal.textContent = cursorA_Ms !== null ? cursorA_Ms.toFixed(1) + ' ms' : '-- ms';
      if (cursorBVal) cursorBVal.textContent = cursorB_Ms !== null ? cursorB_Ms.toFixed(1) + ' ms' : '-- ms';

      if (cursorA_Ms !== null && cursorB_Ms !== null) {
        const delta = Math.abs(cursorB_Ms - cursorA_Ms);
        if (deltaTVal) deltaTVal.textContent = delta.toFixed(1) + ' ms';
        if (deltaFVal) {
          const freqHz = delta > 0 ? (1000 / delta) : 0;
          deltaFVal.textContent = freqHz >= 1000 ? (freqHz / 1000).toFixed(2) + ' kHz' : freqHz.toFixed(1) + ' Hz';
        }
      }
    }

    function recordSample(pinsState) {
      const ts = Date.now();
      sampleBuffer.push({ ts: ts, pins: pinsState });
      if (sampleBuffer.length > maxSamples) {
        sampleBuffer.shift();
      }

      activePins.forEach(function(pin) {
        const val = pinsState[pin] !== undefined ? pinsState[pin] : 0;
        const st = edgeStats[pin];
        if (st) {
          if (st.lastState !== -1 && st.lastState !== val) {
            st.transitions++;
          }
          st.lastState = val;
          if (val === 1) st.highCount++;
          st.totalCount++;
        }
      });
    }

    function renderMetrics() {
      if (!metricsGrid) return;
      metricsGrid.innerHTML = '';
      Array.from(selectedPins).forEach(function(pin) {
        const st = edgeStats[pin] || { transitions: 0, highCount: 0, totalCount: 1, lastState: 0 };
        const duty = st.totalCount > 0 ? Math.round((st.highCount / st.totalCount) * 100) : 0;
        const color = pinColors[pin] || '#7c8aff';

        const card = document.createElement('div');
        card.className = 'metric-card';
        card.style.borderLeftColor = color;
        card.innerHTML = '<div class="metric-header">' +
          '<span class="metric-title" style="color:' + color + '">' + (pinLabels[pin] || ('GPIO' + pin)) + '</span>' +
          '<span class="metric-state ' + (st.lastState === 1 ? 'high' : 'low') + '">' + (st.lastState === 1 ? 'HIGH (1)' : 'LOW (0)') + '</span>' +
          '</div>' +
          '<div class="metric-body">' +
          '<div class="metric-row"><span>Edges:</span> <strong>' + st.transitions + '</strong></div>' +
          '<div class="metric-row"><span>Duty Cycle:</span> <strong>' + duty + '%</strong></div>' +
          '</div>';
        metricsGrid.appendChild(card);
      });
    }

    let tickCount = 0;
    function generateDemoSample() {
      tickCount++;
      const pins = {};
      pins[2] = (tickCount % 6 < 3) ? 1 : 0;
      pins[3] = (tickCount % 2 === 0) ? 1 : 0;
      pins[4] = (tickCount % 20 < 2 || tickCount % 20 === 10) ? 1 : 0;
      pins[14] = (tickCount % 12 < 4) ? 1 : 0;
      pins[15] = (tickCount % 16 < 8) ? 1 : 0;
      pins[17] = (tickCount % 10 < 5) ? 1 : 0;
      pins[18] = (tickCount % 4 < 2) ? 1 : 0;
      pins[22] = (tickCount % 8 < 4) ? 1 : 0;
      pins[23] = (tickCount % 14 < 7) ? 1 : 0;
      pins[24] = (tickCount % 18 < 9) ? 1 : 0;
      pins[25] = (tickCount % 24 < 12) ? 1 : 0;
      pins[27] = (tickCount % 30 < 15) ? 1 : 0;
      return pins;
    }

    let fetchInterval = null;
    function startSamplingLoop() {
      if (fetchInterval) clearInterval(fetchInterval);
      fetchInterval = setInterval(async function() {
        if (!isRunning) return;

        if (isTestGen) {
          recordSample(generateDemoSample());
        } else {
          try {
            const res = await fetch('/gpio/status');
            if (res.ok) {
              const html = await res.text();
              const pinsState = {};
              const parser = new DOMParser();
              const doc = parser.parseFromString(html, 'text/html');
              const rows = doc.querySelectorAll('.gpio-header-table tbody tr');
              rows.forEach(function(tr) {
                const btns = tr.querySelectorAll('.ctrl-col button');
                btns.forEach(function(btn) {
                  const vals = btn.getAttribute('hx-vals');
                  if (vals) {
                    try {
                      const parsed = JSON.parse(vals);
                      if (parsed.bcm !== undefined) {
                        const stateBadge = btn.closest('td').parentElement.querySelectorAll('.pin-state-badge');
                        stateBadge.forEach(function(badge) {
                          const isHigh = badge.textContent.includes('HIGH') || badge.classList.contains('high');
                          pinsState[parsed.bcm] = isHigh ? 1 : 0;
                        });
                      }
                    } catch(e){}
                  }
                });
              });
              activePins.forEach(function(p) {
                if (pinsState[p] === undefined) pinsState[p] = 0;
              });
              recordSample(pinsState);
            }
          } catch(e){}
        }
      }, sampleIntervalMs);
    }
    startSamplingLoop();

    function renderCanvasFrame() {
      const width = canvas.width;
      const height = canvas.height;
      ctx.clearRect(0, 0, width, height);

      ctx.fillStyle = '#0b0d14';
      ctx.fillRect(0, 0, width, height);

      const channels = Array.from(selectedPins);
      const activeDecoder = decoderSelect ? decoderSelect.value : 'none';
      const hasDecoderTrack = activeDecoder !== 'none';
      const decoderTrackHeight = hasDecoderTrack ? 40 : 0;

      const topPadding = 35;
      const bottomPadding = 35 + decoderTrackHeight;
      const leftMargin = 150;
      const rightMargin = 25;
      const plotWidth = width - leftMargin - rightMargin;
      const plotHeight = height - topPadding - bottomPadding;
      const channelHeight = channels.length > 0 ? plotHeight / channels.length : plotHeight;

      const numDivs = 10;
      const divWidth = plotWidth / numDivs;

      ctx.strokeStyle = 'rgba(0, 220, 255, 0.04)';
      ctx.lineWidth = 1;
      for (let i = 0; i <= numDivs * 5; i++) {
        const x = leftMargin + i * (divWidth / 5);
        ctx.beginPath();
        ctx.moveTo(x, topPadding);
        ctx.lineTo(x, height - bottomPadding);
        ctx.stroke();
      }

      ctx.strokeStyle = 'rgba(0, 220, 255, 0.14)';
      ctx.fillStyle = '#89b4fa';
      ctx.font = '11px monospace';

      const totalWindowMs = timebaseMsPerDiv * numDivs;
      const now = Date.now();
      const startTime = now - totalWindowMs;

      for (let i = 0; i <= numDivs; i++) {
        const x = leftMargin + i * divWidth;
        ctx.beginPath();
        ctx.moveTo(x, topPadding);
        ctx.lineTo(x, height - bottomPadding);
        ctx.stroke();

        const timeLabel = '-' + Math.round((numDivs - i) * timebaseMsPerDiv) + 'ms';
        ctx.fillText(timeLabel, x - 18, height - 12);
        ctx.fillText(timeLabel, x - 18, 20);
      }

      channels.forEach(function(pin, index) {
        const channelTop = topPadding + index * channelHeight;
        const channelBottom = channelTop + channelHeight;
        const signalHighY = channelTop + channelHeight * 0.22;
        const signalLowY = channelTop + channelHeight * 0.78;
        const color = pinColors[pin] || '#7c8aff';

        ctx.strokeStyle = 'rgba(255, 255, 255, 0.07)';
        ctx.beginPath();
        ctx.moveTo(leftMargin, channelBottom);
        ctx.lineTo(width - rightMargin, channelBottom);
        ctx.stroke();

        ctx.fillStyle = 'rgba(255, 255, 255, 0.03)';
        ctx.fillRect(10, channelTop + 4, leftMargin - 20, channelHeight - 8);

        ctx.fillStyle = color;
        ctx.fillRect(12, channelTop + 8, 5, channelHeight - 16);

        ctx.fillStyle = '#cdd6f4';
        ctx.font = 'bold 12px sans-serif';
        ctx.fillText(pinLabels[pin] || ('GPIO' + pin), 24, channelTop + channelHeight / 2 + 4);

        const st = edgeStats[pin] || { lastState: 0 };
        ctx.fillStyle = st.lastState === 1 ? 'rgba(76, 175, 80, 0.2)' : 'rgba(255, 255, 255, 0.08)';
        ctx.fillRect(leftMargin - 45, channelTop + (channelHeight - 18) / 2, 35, 18);
        ctx.fillStyle = st.lastState === 1 ? '#4caf50' : '#8080a0';
        ctx.font = 'bold 10px monospace';
        ctx.fillText(st.lastState === 1 ? 'HIGH' : 'LOW', leftMargin - 40, channelTop + channelHeight / 2 + 3);

        if (sampleBuffer.length > 1) {
          ctx.strokeStyle = color;
          ctx.lineWidth = 2;
          ctx.shadowColor = color;
          ctx.shadowBlur = 6;
          ctx.beginPath();

          let prevX = null;
          let prevY = null;

          for (let i = 0; i < sampleBuffer.length; i++) {
            const sample = sampleBuffer[i];
            const val = (sample.pins && sample.pins[pin] !== undefined) ? sample.pins[pin] : 0;
            const timeOffset = sample.ts - startTime;
            const x = leftMargin + (timeOffset / totalWindowMs) * plotWidth;

            if (x < leftMargin) continue;
            if (x > width - rightMargin) break;

            const y = val === 1 ? signalHighY : signalLowY;

            if (prevX === null) {
              ctx.moveTo(x, y);
            } else {
              ctx.lineTo(x, prevY);
              ctx.lineTo(x, y);
            }
            prevX = x;
            prevY = y;
          }
          ctx.stroke();
          ctx.shadowBlur = 0;
        }
      });

      if (hasDecoderTrack) {
        const decTop = height - bottomPadding + 5;
        const decHeight = decoderTrackHeight - 10;
        ctx.fillStyle = 'rgba(0, 188, 212, 0.08)';
        ctx.fillRect(leftMargin, decTop, plotWidth, decHeight);

        ctx.fillStyle = '#00bcd4';
        ctx.font = 'bold 11px sans-serif';
        ctx.fillText(activeDecoder === 'i2c' ? 'I2C DECODER' : 'UART DECODER', 24, decTop + decHeight / 2 + 4);

        const bubbleStep = plotWidth / 6;
        for (let b = 0; b < 6; b++) {
          const bx = leftMargin + b * bubbleStep + 10;
          const bw = bubbleStep - 20;
          const by = decTop + 3;
          const bh = decHeight - 6;

          ctx.fillStyle = b === 0 ? 'rgba(233, 30, 99, 0.3)' : (b === 5 ? 'rgba(76, 175, 80, 0.3)' : 'rgba(0, 188, 212, 0.3)');
          ctx.strokeStyle = b === 0 ? '#e91e63' : (b === 5 ? '#4caf50' : '#00bcd4');
          ctx.lineWidth = 1.5;

          ctx.beginPath();
          if (ctx.roundRect) {
            ctx.roundRect(bx, by, bw, bh, 6);
          } else {
            ctx.rect(bx, by, bw, bh);
          }
          ctx.fill();
          ctx.stroke();

          ctx.fillStyle = '#cdd6f4';
          ctx.font = 'bold 10px monospace';
          const pktText = activeDecoder === 'i2c'
            ? (b === 0 ? 'START' : (b === 5 ? 'STOP' : '0x48 [ACK]'))
            : ('0x' + (40 + b * 5).toString(16).toUpperCase() + ' ' + String.fromCharCode(65 + b));
          ctx.fillText(pktText, bx + 8, by + bh / 2 + 3);
        }
      }

      if (cursorA_Ms !== null) {
        const curAx = leftMargin + ((cursorA_Ms + totalWindowMs) / totalWindowMs) * plotWidth;
        if (curAx >= leftMargin && curAx <= width - rightMargin) {
          ctx.strokeStyle = '#00bcd4';
          ctx.lineWidth = 2;
          ctx.setLineDash([4, 4]);
          ctx.beginPath();
          ctx.moveTo(curAx, topPadding);
          ctx.lineTo(curAx, height - bottomPadding);
          ctx.stroke();
          ctx.setLineDash([]);

          ctx.fillStyle = '#00bcd4';
          ctx.font = 'bold 10px monospace';
          ctx.fillText('A: ' + cursorA_Ms.toFixed(1) + 'ms', curAx + 4, topPadding + 15);
        }
      }

      if (cursorB_Ms !== null) {
        const curBx = leftMargin + ((cursorB_Ms + totalWindowMs) / totalWindowMs) * plotWidth;
        if (curBx >= leftMargin && curBx <= width - rightMargin) {
          ctx.strokeStyle = '#ffc107';
          ctx.lineWidth = 2;
          ctx.setLineDash([4, 4]);
          ctx.beginPath();
          ctx.moveTo(curBx, topPadding);
          ctx.lineTo(curBx, height - bottomPadding);
          ctx.stroke();
          ctx.setLineDash([]);

          ctx.fillStyle = '#ffc107';
          ctx.font = 'bold 10px monospace';
          ctx.fillText('B: ' + cursorB_Ms.toFixed(1) + 'ms', curBx + 4, topPadding + 30);
        }
      }

      if (mouseX >= leftMargin && mouseX <= width - rightMargin) {
        ctx.strokeStyle = 'rgba(255, 255, 255, 0.4)';
        ctx.setLineDash([3, 3]);
        ctx.beginPath();
        ctx.moveTo(mouseX, topPadding);
        ctx.lineTo(mouseX, height - bottomPadding);
        ctx.stroke();
        ctx.setLineDash([]);

        const cursorTimeOffsetMs = ((mouseX - leftMargin) / plotWidth) * totalWindowMs - totalWindowMs;
        if (scopeCursorInfo) {
          scopeCursorInfo.style.display = 'block';
          scopeCursorInfo.style.left = (mouseX + 15) + 'px';
          scopeCursorInfo.style.top = (mouseY || 40) + 'px';
          scopeCursorInfo.innerHTML = '<strong>t = ' + Math.round(cursorTimeOffsetMs) + ' ms</strong>';
        }
      }

      requestAnimationFrame(renderCanvasFrame);
    }

    requestAnimationFrame(renderCanvasFrame);

    if (btnToggleRun) {
      btnToggleRun.addEventListener('click', function() {
        isRunning = !isRunning;
        btnToggleRun.textContent = isRunning ? '⏸ Pause' : '▶ Run';
        btnToggleRun.classList.toggle('run', isRunning);
        if (scopeStatus) {
          scopeStatus.textContent = isRunning ? '● LIVE 60 FPS' : '⏸ PAUSED';
          scopeStatus.classList.toggle('live', isRunning);
        }
      });
    }

    if (btnClearScope) {
      btnClearScope.addEventListener('click', function() {
        sampleBuffer = [];
        cursorA_Ms = null;
        cursorB_Ms = null;
        updateDeltaBar();
        activePins.forEach(function(pin) {
          edgeStats[pin] = { transitions: 0, lastState: -1, highCount: 0, totalCount: 0 };
        });
        renderMetrics();
      });
    }

    if (btnZoomIn) {
      btnZoomIn.addEventListener('click', function() {
        if (timebaseMsPerDiv > 10) timebaseMsPerDiv = Math.max(10, Math.round(timebaseMsPerDiv / 2));
        if (timebaseSelect) timebaseSelect.value = timebaseMsPerDiv;
      });
    }

    if (btnZoomOut) {
      btnZoomOut.addEventListener('click', function() {
        if (timebaseMsPerDiv < 2000) timebaseMsPerDiv = Math.min(2000, timebaseMsPerDiv * 2);
        if (timebaseSelect) timebaseSelect.value = timebaseMsPerDiv;
      });
    }

    if (btnResetView) {
      btnResetView.addEventListener('click', function() {
        timebaseMsPerDiv = 100;
        cursorA_Ms = null;
        cursorB_Ms = null;
        updateDeltaBar();
        if (timebaseSelect) timebaseSelect.value = 100;
      });
    }

    if (btnTestGen) {
      btnTestGen.addEventListener('click', function() {
        isTestGen = !isTestGen;
        btnTestGen.textContent = isTestGen ? '⚡ Demo Signal Generator' : '🔌 Hardware Live Mode';
        btnTestGen.classList.toggle('accent', isTestGen);
      });
    }

    if (timebaseSelect) {
      timebaseSelect.addEventListener('change', function(e) {
        timebaseMsPerDiv = parseInt(e.target.value, 10);
      });
    }

    if (btnExportVcd) {
      btnExportVcd.addEventListener('click', function() {
        const vcdLines = [
          '', '  ' + new Date().toISOString(), '',
          '', '  PiScope Web VCD Exporter 3.0', '',
          ' 1ms ',
          ' module piscope '
        ];

        const channels = Array.from(selectedPins);
        channels.forEach(function(pin) {
          vcdLines.push(' wire 1 g' + pin + ' GPIO' + pin + ' ');
        });
        vcdLines.push(' ', ' ');

        if (sampleBuffer.length > 0) {
          const baseTs = sampleBuffer[0].ts;
          sampleBuffer.forEach(function(sample) {
            const relTs = Math.round(sample.ts - baseTs);
            vcdLines.push('#' + relTs);
            channels.forEach(function(pin) {
              const val = sample.pins && sample.pins[pin] !== undefined ? sample.pins[pin] : 0;
              vcdLines.push(val + 'g' + pin);
            });
          });
        }

        const blob = new Blob([vcdLines.join(String.fromCharCode(10)) + String.fromCharCode(10)], { type: 'text/plain' });
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = 'piscope_capture_' + Date.now() + '.vcd';
        a.click();
        URL.revokeObjectURL(url);
      });
    }

    if (btnExportCsv) {
      btnExportCsv.addEventListener('click', function() {
        const channels = Array.from(selectedPins);
        const csvLines = ['Timestamp_ms,' + channels.map(function(p) { return 'GPIO' + p; }).join(',')];

        if (sampleBuffer.length > 0) {
          const baseTs = sampleBuffer[0].ts;
          sampleBuffer.forEach(function(sample) {
            const relTs = Math.round(sample.ts - baseTs);
            const row = channels.map(function(p) { return (sample.pins && sample.pins[p] !== undefined) ? sample.pins[p] : 0; });
            csvLines.push(relTs + ',' + row.join(','));
          });
        }

        const blob = new Blob([csvLines.join(String.fromCharCode(10)) + String.fromCharCode(10)], { type: 'text/csv' });
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = 'piscope_capture_' + Date.now() + '.csv';
        a.click();
        URL.revokeObjectURL(url);
      });
    }
  }

  if (document.readyState === 'complete' || document.readyState === 'interactive') {
    initPiScope();
  } else {
    document.addEventListener('DOMContentLoaded', initPiScope);
  }
})();