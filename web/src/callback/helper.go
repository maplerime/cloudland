package callback

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/jinzhu/gorm"
)

func compactSQL(s string) string {
	ss := strings.TrimSpace(s)
	ss = strings.ReplaceAll(ss, "\n", " ")
	ss = strings.ReplaceAll(ss, "\t", " ")
	for strings.Contains(ss, "  ") {
		ss = strings.ReplaceAll(ss, "  ", " ")
	}
	return ss
}

func setStmtTimeoutWithFallback(db *gorm.DB, traceID string, d time.Duration) {
	ms := int(d / time.Millisecond)

	// 先尝试 SET LOCAL（和你现有日志行为一致）
	if err := db.Exec("SET LOCAL statement_timeout = ?", ms).Error; err == nil {
		logger.Debugf("[%s] statement_timeout set via SET LOCAL = %dms", traceID, ms)
		return
	} else {
		logger.Debugf("[%s] SET LOCAL statement_timeout failed (maybe not in tx): %v, fallback to SET", traceID, err)
	}

	// fallback：SET（对当前会话生效，后续建议你在调用后再恢复）
	if err := db.Exec("SET statement_timeout = ?", ms).Error; err != nil {
		logger.Errorf("[%s] SET statement_timeout failed: %v", traceID, err)
		return
	}
	logger.Debugf("[%s] statement_timeout set via SET = %dms", traceID, ms)
}

func printDBStatsV1(gdb *gorm.DB, traceID string) {
	sqlDB := gdb.DB()
	if sqlDB == nil {
		logger.Errorf("[%s] dbstats: gdb.DB() is nil", traceID)
		return
	}
	st := sqlDB.Stats()
	logger.Errorf("[%s] dbstats: open=%d inuse=%d idle=%d waitcount=%d waittime=%s maxopen=%d maxidleclosed=%d maxlifetimeclosed=%d",
		traceID,
		st.OpenConnections, st.InUse, st.Idle,
		st.WaitCount, st.WaitDuration,
		st.MaxOpenConnections, st.MaxIdleClosed, st.MaxLifetimeClosed,
	)
}

// 超时/失败时旁路诊断：pg_stat_activity / blocking chain / waiting locks
func dumpPostgresDiagnostics(traceID string) {
	diag := getDiagDB()
	if diag == nil {
		logger.Errorf("[%s] pg_diagnose: diagDB is nil (call InitDiagDB first)", traceID)
		return
	}

	// 小超时，避免诊断本身卡住
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	out, err := diagnosePostgres(ctx, diag)
	if err != nil {
		logger.Errorf("[%s] pg_diagnose: failed: %v", traceID, err)
		return
	}
	logger.Errorf("[%s] pg_diagnose:\n%s", traceID, out)
}

func diagnosePostgres(ctx context.Context, db *sql.DB) (string, error) {
	const q1 = `
SELECT
  pid, usename, application_name, client_addr,
  state, wait_event_type, wait_event,
  now()-xact_start   AS xact_age,
  now()-query_start  AS query_age,
  now()-state_change AS state_age,
  left(query, 160)   AS query
FROM pg_stat_activity
WHERE datname = current_database()
ORDER BY COALESCE(xact_start, query_start) NULLS LAST;
`
	const q2 = `
SELECT
  blocked.pid AS blocked_pid,
  blocking.pid AS blocking_pid,
  now()-blocked.query_start AS blocked_for,
  left(blocked.query, 160) AS blocked_query,
  now()-blocking.query_start AS blocking_for,
  left(blocking.query, 160) AS blocking_query
FROM pg_stat_activity blocked
JOIN pg_stat_activity blocking
  ON blocking.pid = ANY(pg_blocking_pids(blocked.pid))
WHERE blocked.datname = current_database()
ORDER BY blocked_for DESC;
`
	const q3 = `
SELECT
  a.pid, a.state, a.wait_event_type, a.wait_event,
  l.locktype, l.mode, l.granted,
  c.relname,
  left(a.query, 120) AS query
FROM pg_locks l
JOIN pg_stat_activity a ON a.pid=l.pid
LEFT JOIN pg_class c ON c.oid=l.relation
WHERE a.datname=current_database()
  AND l.granted=false
ORDER BY a.query_start;
`

	var sb strings.Builder
	sb.WriteString("---- pg_stat_activity ----\n")
	t1, err := queryText(ctx, db, q1)
	if err != nil {
		return "", err
	}
	sb.WriteString(t1)

	sb.WriteString("\n---- blocking chain ----\n")
	t2, err := queryText(ctx, db, q2)
	if err != nil {
		return "", err
	}
	sb.WriteString(t2)

	sb.WriteString("\n---- waiting locks (granted=false) ----\n")
	t3, err := queryText(ctx, db, q3)
	if err != nil {
		return "", err
	}
	sb.WriteString(t3)

	return sb.String(), nil
}

func queryText(ctx context.Context, db *sql.DB, q string) (string, error) {
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return "", err
	}

	vals := make([]interface{}, len(cols))
	ptrs := make([]interface{}, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}

	var sb strings.Builder
	sb.WriteString(strings.Join(cols, "\t"))
	sb.WriteString("\n")

	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return "", err
		}
		for i, v := range vals {
			if i > 0 {
				sb.WriteString("\t")
			}
			sb.WriteString(fmt.Sprintf("%v", v))
		}
		sb.WriteString("\n")
	}
	return sb.String(), rows.Err()
}
