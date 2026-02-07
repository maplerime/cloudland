package callback

import (
	"database/sql"
	"sync"
	"time"

	_ "github.com/lib/pq"
	"github.com/spf13/viper"
)

var (
	diagOnce sync.Once
	diagDB   *sql.DB
)

// InitDiagDB 初始化旁路诊断连接（建议程序启动时调用一次）
// dsn 例： "host=127.0.0.1 port=5432 user=postgres password=xxx dbname=cloudland sslmode=disable"
func InitDiagDB(dsn string) {
	diagOnce.Do(func() {
		db, err := sql.Open("postgres", viper.GetString("db.uri"))
		if err != nil {
			logger.Errorf("InitDiagDB: sql.Open failed: %v", err)
			return
		}
		// 小池子即可，避免影响业务
		db.SetMaxOpenConns(5)
		db.SetMaxIdleConns(2)
		db.SetConnMaxLifetime(5 * time.Minute)
		db.SetConnMaxIdleTime(2 * time.Minute)

		diagDB = db
		logger.Infof("InitDiagDB: initialized")
	})
}

func getDiagDB() *sql.DB {
	return diagDB
}
