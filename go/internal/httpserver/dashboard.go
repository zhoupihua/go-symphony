package httpserver

import (
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"time"
)

type dashboardData struct {
	Leader        bool
	LeaderAddr    string
	RunningCount  int
	MaxConcurrent int
	Running       []runInfoRow
	RetryQueue    []retryRow
	TotalUsage    usageRow
	Now           time.Time
}

type runInfoRow struct {
	Identifier    string
	Title         string
	State         string
	Attempt       int
	TurnCount     int
	StartedAt     time.Time
	WorkspacePath string
	InputTokens   int64
	OutputTokens  int64
	TotalTokens   int64
}

type retryRow struct {
	Identifier string
	Attempt    int
	FireAt     time.Time
	IsContinue bool
}

type usageRow struct {
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
}

var dashboardTmpl = template.Must(template.New("dashboard").Funcs(template.FuncMap{
	"reltime": func(t, now time.Time) string {
		d := now.Sub(t)
		if d < time.Minute {
			return "just now"
		}
		if d < time.Hour {
			return fmt.Sprintf("%dm ago", int(d.Minutes()))
		}
		if d < 24*time.Hour {
			return fmt.Sprintf("%dh %dm ago", int(d.Hours()), int(d.Minutes())%60)
		}
		return t.Format("Jan 02 15:04")
	},
}).Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Symphony Dashboard</title>
<script src="https://unpkg.com/htmx.org@2.0.4"></script>
<style>
*{margin:0;padding:0;box-sizing:border-box}
body{background:#1a1a2e;color:#e0e0e0;font-family:'SF Mono',Consolas,'Liberation Mono',Menlo,monospace;font-size:13px;padding:16px}
h1{font-size:18px;color:#00d4ff;margin-bottom:12px}
.status{display:flex;gap:24px;margin-bottom:12px;align-items:center}
.badge{display:inline-block;padding:3px 8px;border-radius:3px;font-weight:700;font-size:12px}
.badge-leader{background:#0d7377;color:#fff}
.badge-standby{background:#6b3a2a;color:#ffb347}
.count{color:#aaa;font-size:13px}
.count strong{color:#00d4ff}
button{background:#0d7377;color:#fff;border:none;padding:6px 14px;border-radius:3px;cursor:pointer;font-family:inherit;font-size:12px}
button:hover{background:#0a5c5f}
.cards{display:flex;gap:16px;margin-bottom:12px;flex-wrap:wrap}
.card{background:#16213e;border:1px solid #0f3460;border-radius:4px;padding:10px 14px;min-width:140px}
.card-label{color:#888;font-size:11px;text-transform:uppercase;letter-spacing:.5px;margin-bottom:4px}
.card-value{color:#00d4ff;font-size:18px;font-weight:700}
.card-sub{color:#666;font-size:11px;margin-top:2px}
table{width:100%;border-collapse:collapse;margin-top:12px}
th{text-align:left;color:#888;border-bottom:1px solid #333;padding:6px 8px;font-weight:600;font-size:11px;text-transform:uppercase;letter-spacing:.5px}
td{padding:6px 8px;border-bottom:1px solid #222}
td.identifier{color:#00d4ff;font-weight:600}
td.title{max-width:300px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.empty{color:#666;padding:20px;text-align:center}
.leader-addr{color:#ffb347;font-size:12px}
.section-title{color:#aaa;font-size:13px;font-weight:600;margin-top:16px;margin-bottom:6px;border-bottom:1px solid #333;padding-bottom:4px}
.badge-continue{background:#2a6b3a;color:#8f8;font-size:10px;padding:2px 5px;border-radius:2px}
.badge-retry{background:#6b3a2a;color:#ffb347;font-size:10px;padding:2px 5px;border-radius:2px}
</style>
</head>
<body>
<h1>Symphony</h1>
<div id="dashboard">
{{template "status" .}}
{{template "cards" .}}
<div id="running-section">
{{template "table" .}}
</div>
<div id="retry-section">
{{template "retry" .}}
</div>
</div>

{{define "status"}}
<div class="status">
{{if .Leader}}<span class="badge badge-leader">LEADER</span>{{else}}<span class="badge badge-standby">STANDBY</span>{{end}}
<span class="count">Running: <strong>{{.RunningCount}}</strong> / {{.MaxConcurrent}}</span>
{{if not .Leader}}<span class="leader-addr">Leader: {{.LeaderAddr}}</span>{{end}}
<button hx-post="/api/v1/refresh" hx-swap="none">Refresh</button>
</div>
{{end}}

{{define "cards"}}
<div class="cards">
<div class="card">
<div class="card-label">Active</div>
<div class="card-value">{{.RunningCount}}</div>
<div class="card-sub">of {{.MaxConcurrent}} slots</div>
</div>
<div class="card">
<div class="card-label">Input Tokens</div>
<div class="card-value">{{.TotalUsage.InputTokens}}</div>
</div>
<div class="card">
<div class="card-label">Output Tokens</div>
<div class="card-value">{{.TotalUsage.OutputTokens}}</div>
</div>
<div class="card">
<div class="card-label">Total Tokens</div>
<div class="card-value">{{.TotalUsage.TotalTokens}}</div>
</div>
<div class="card">
<div class="card-label">Retry Queue</div>
<div class="card-value">{{len .RetryQueue}}</div>
</div>
</div>
{{end}}

{{define "table"}}
{{if .Running}}
<div class="section-title">Running Issues</div>
<table>
<thead><tr><th>ID</th><th>Title</th><th>State</th><th>Attempt</th><th>Turns</th><th>Tokens</th><th>Started</th><th>Workspace</th></tr></thead>
<tbody>
{{range .Running}}
<tr>
<td class="identifier">{{.Identifier}}</td>
<td class="title">{{.Title}}</td>
<td>{{.State}}</td>
<td>{{.Attempt}}</td>
<td>{{.TurnCount}}</td>
<td>{{.TotalTokens}}</td>
<td>{{reltime .StartedAt $.Now}}</td>
<td>{{.WorkspacePath}}</td>
</tr>
{{end}}
</tbody>
</table>
{{else}}
<div class="empty">No running issues</div>
{{end}}
{{end}}

{{define "retry"}}
{{if .RetryQueue}}
<div class="section-title">Retry Queue</div>
<table>
<thead><tr><th>ID</th><th>Attempt</th><th>Fire In</th><th>Type</th></tr></thead>
<tbody>
{{range .RetryQueue}}
<tr>
<td class="identifier">{{.Identifier}}</td>
<td>{{.Attempt}}</td>
<td>{{reltime .FireAt $.Now}}</td>
<td>{{if .IsContinue}}<span class="badge-continue">continue</span>{{else}}<span class="badge-retry">failure</span>{{end}}</td>
</tr>
{{end}}
</tbody>
</table>
{{end}}
{{end}}

<script>
window.__maxConcurrent = {{.MaxConcurrent}};
(function(){
  var es = new EventSource("/api/v1/events");
  es.addEventListener("state", function(e) {
    var data = JSON.parse(e.data);
    updateDashboard(data);
  });

  function updateDashboard(data) {
    var statusEl = document.querySelector(".status");
    if (statusEl) {
      var html = '';
      if (data.leader) {
        html += '<span class="badge badge-leader">LEADER</span>';
      } else {
        html += '<span class="badge badge-standby">STANDBY</span>';
      }
      html += '<span class="count">Running: <strong>' + data.running_count + '</strong> / ' + (window.__maxConcurrent || 10) + '</span>';
      if (!data.leader) {
        html += '<span class="leader-addr">Leader: ' + (data.leader_addr || 'unknown') + '</span>';
      }
      html += '<button hx-post="/api/v1/refresh" hx-swap="none">Refresh</button>';
      statusEl.innerHTML = html;
      htmx.process(statusEl);
    }

    // Update cards.
    var cardsEl = document.querySelector(".cards");
    if (cardsEl) {
      var totIn = 0, totOut = 0, totAll = 0;
      var keys = Object.keys(data.running || {});
      for (var i = 0; i < keys.length; i++) {
        var r = data.running[keys[i]];
        totIn += (r.input_tokens || 0);
        totOut += (r.output_tokens || 0);
        totAll += (r.total_tokens || 0);
      }
      var retryCount = (data.retry_queue || []).length;
      cardsEl.innerHTML =
        '<div class="card"><div class="card-label">Active</div><div class="card-value">' + data.running_count + '</div><div class="card-sub">of ' + (window.__maxConcurrent || 10) + ' slots</div></div>' +
        '<div class="card"><div class="card-label">Input Tokens</div><div class="card-value">' + totIn + '</div></div>' +
        '<div class="card"><div class="card-label">Output Tokens</div><div class="card-value">' + totOut + '</div></div>' +
        '<div class="card"><div class="card-label">Total Tokens</div><div class="card-value">' + totAll + '</div></div>' +
        '<div class="card"><div class="card-label">Retry Queue</div><div class="card-value">' + retryCount + '</div></div>';
    }

    // Update running table.
    var rkeys = Object.keys(data.running || {});
    var runningSection = document.getElementById("running-section");
    var tableEl = document.querySelector("#running-table tbody");
    if (rkeys.length === 0) {
      if (runningSection) runningSection.innerHTML = '<div class="empty">No running issues</div>';
      return;
    }

    var tbody = '';
    var now = new Date();
    for (var i = 0; i < rkeys.length; i++) {
      var r = data.running[rkeys[i]];
      tbody += '<tr>';
      tbody += '<td class="identifier">' + esc(r.identifier) + '</td>';
      tbody += '<td class="title">' + esc(r.title) + '</td>';
      tbody += '<td>' + esc(r.state) + '</td>';
      tbody += '<td>' + r.attempt + '</td>';
      tbody += '<td>' + r.turn_count + '</td>';
      tbody += '<td>' + (r.total_tokens || 0) + '</td>';
      tbody += '<td>' + reltime(r.started_at, now) + '</td>';
      tbody += '<td>' + esc(r.workspace_path) + '</td>';
      tbody += '</tr>';
    }

    if (runningSection) {
      runningSection.innerHTML = '<div class="section-title">Running Issues</div><table id="running-table"><thead><tr><th>ID</th><th>Title</th><th>State</th><th>Attempt</th><th>Turns</th><th>Tokens</th><th>Started</th><th>Workspace</th></tr></thead><tbody>' + tbody + '</tbody></table>';
    }

    // Update retry table.
    var retrySection = document.getElementById("retry-section");
    var retries = data.retry_queue || [];
    if (retrySection) {
      if (retries.length === 0) {
        retrySection.innerHTML = '';
      } else {
        var rtbody = '';
        for (var i = 0; i < retries.length; i++) {
          var rq = retries[i];
          rtbody += '<tr>';
          rtbody += '<td class="identifier">' + esc(rq.identifier) + '</td>';
          rtbody += '<td>' + rq.attempt + '</td>';
          rtbody += '<td>' + reltime(rq.fire_at, now) + '</td>';
          rtbody += '<td>' + (rq.is_continue ? '<span class="badge-continue">continue</span>' : '<span class="badge-retry">failure</span>') + '</td>';
          rtbody += '</tr>';
        }
        retrySection.innerHTML = '<div class="section-title">Retry Queue</div><table><thead><tr><th>ID</th><th>Attempt</th><th>Fire In</th><th>Type</th></tr></thead><tbody>' + rtbody + '</tbody></table>';
      }
    }
  }

  function esc(s) {
    var d = document.createElement('div');
    d.textContent = s || '';
    return d.innerHTML;
  }

  function reltime(iso, now) {
    var t = new Date(iso);
    var d = now - t;
    if (d < 60000) return 'just now';
    if (d < 3600000) return Math.floor(d / 60000) + 'm ago';
    if (d < 86400000) return Math.floor(d / 3600000) + 'h ' + Math.floor((d % 3600000) / 60000) + 'm ago';
    return t.toLocaleDateString();
  }
})();
</script>
</body>
</html>
`))

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	// Standby redirect: if not leader and leader address is known, redirect.
	if !s.isLeader() {
		leaderAddr := ""
		if s.elector != nil {
			leaderAddr = s.elector.LeaderAddr()
		}
		if leaderAddr != "" {
			http.Redirect(w, r, "http://"+leaderAddr+"/", http.StatusTemporaryRedirect)
			return
		}
	}

	running := s.state.Running()

	var totalUsage usageRow
	rows := make([]runInfoRow, 0, len(running))
	for _, info := range running {
		rows = append(rows, runInfoRow{
			Identifier:    info.Issue.Identifier,
			Title:         info.Issue.Title,
			State:         info.Issue.State,
			Attempt:       info.Attempt,
			TurnCount:     info.TurnCount,
			StartedAt:     info.StartedAt,
			WorkspacePath: info.WorkspacePath,
			InputTokens:   info.TotalUsage.InputTokens,
			OutputTokens:  info.TotalUsage.OutputTokens,
			TotalTokens:   info.TotalUsage.TotalTokens,
		})
		totalUsage.InputTokens += info.TotalUsage.InputTokens
		totalUsage.OutputTokens += info.TotalUsage.OutputTokens
		totalUsage.TotalTokens += info.TotalUsage.TotalTokens
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].StartedAt.After(rows[j].StartedAt)
	})

	// Build retry queue rows.
	pendingRetries := s.state.PendingRetries()
	var retryRows []retryRow
	if len(pendingRetries) > 0 {
		retryRows = make([]retryRow, 0, len(pendingRetries))
		for _, entry := range pendingRetries {
			retryRows = append(retryRows, retryRow{
				Identifier: entry.Issue.Identifier,
				Attempt:    entry.Attempt,
				FireAt:     entry.FireAt,
				IsContinue: entry.IsContinue,
			})
		}
	}
	if retryRows == nil {
		retryRows = []retryRow{}
	}

	leaderAddr := ""
	if s.elector != nil {
		leaderAddr = s.elector.LeaderAddr()
	}

	data := dashboardData{
		Leader:        s.isLeader(),
		LeaderAddr:    leaderAddr,
		RunningCount:  s.state.RunningCount(),
		MaxConcurrent: s.maxConcurrent,
		Running:       rows,
		RetryQueue:    retryRows,
		TotalUsage:    totalUsage,
		Now:           time.Now(),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := dashboardTmpl.Execute(w, data); err != nil {
		http.Error(w, "dashboard render error", http.StatusInternalServerError)
	}
}
