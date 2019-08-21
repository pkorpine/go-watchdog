package lib

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/segmentio/ksuid"
)

type Database struct {
	db *sql.DB
}

type User struct {
	Id   int64
	Name string
	TgId int64
	Key  string
}

type Timer struct {
	// Values in database
	Id       int64  `json:"timerid"`
	UserId   int64  `json:"-"`
	Name     string `json:"name" form:"name" query:"name"`
	Interval int64  `json:"interval" form:"interval" query:"interval"`
	Expiry   int64  `json:"expiry"`
	// State can be "new", "running", "expired"
	State string `json:"state"`

	// Other
	Database *Database `json:"-"`
}

func NewDatabase(dbParameters string) *Database {
	// Open database
	db, err := sql.Open("sqlite3", dbParameters)
	if err != nil {
		log.Fatal(err)
	}

	p := new(Database)
	p.db = db
	return p
}

func (p *Database) Init() {
	// Initialize database
	qs := [...]string{
		`CREATE TABLE IF NOT EXISTS User (
			id     INTEGER PRIMARY KEY AUTOINCREMENT,
			tgname TEXT NOT NULL,
			tgid   TEXT NOT NULL UNIQUE,
			key    TEXT NOT NULL,
			ts     DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS Timer (
			id        INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id   INTEGER NOT NULL,
			name      TEXT NOT NULL,
			interval  INTEGER NOT NULL,
			expiry    INTEGER NOT NULL,
			state     TEXT NOT NULL,
			ts        DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS Event (
			timer_id INTEGER PRIMARY KEY,
			ts       DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS TimerIndexExpiry
			ON Timer (expiry)
			WHERE state="running"
		`,
	}
	for _, q := range qs {
		_, err := p.db.Exec(q)
		if err != nil {
			log.Fatalf("%q: %s\n", err, q)
		}
	}
	log.Println("Database initialized")
}

func (p *Database) Close() {
	p.db.Close()
}

// User entries
func (p *Database) GetUserIdByKey(key string) (id int64, err error) {
	row := p.db.QueryRow(`SELECT id FROM User WHERE key=?`, key)
	err = row.Scan(&id)
	return
}

func (p *Database) GetUserTelegramIdById(id int64) (tgid int64, err error) {
	err = p.db.QueryRow(`SELECT tgid FROM User WHERE id=? LIMIT 1`, id).Scan(&tgid)
	return tgid, err
}

func (p *Database) CreateOrGetUserKeyByTelegramId(u *User) bool {
	row := p.db.QueryRow(`SELECT key FROM User WHERE tgid=?`, u.TgId)
	err := row.Scan(&u.Key)
	switch err {
	case sql.ErrNoRows:
		u.Key = ksuid.New().String()
		_, err := p.db.Exec(`INSERT INTO User (tgname, tgid, key) VALUES (?, ?, ?)`, u.Name, u.TgId, u.Key)
		if err != nil {
			log.Panic(err)
		}
		return true
	case nil:
		return false
	default:
		log.Panic(err)
	}
	return false
}

func (p *Database) NewTimer() *Timer {
	t := &Timer{
		State:    "new",
		Database: p,
	}
	return t
}

func (p *Database) GetTimer(id, userid int64) *Timer {
	row := p.db.QueryRow(`SELECT name, interval, expiry, state FROM Timer WHERE id=? AND user_id=?`, id, userid)

	t := p.NewTimer()
	t.Id = id
	t.UserId = userid
	err := row.Scan(&t.Name, &t.Interval, &t.Expiry, &t.State)

	if err != nil {
		log.Println("WARNING: Timer.Get", id, userid, err)
		return nil
	}

	return t
}

func (p *Database) GetTimersJSON(userid int64) string {
	s := ""
	rows, err := p.db.Query(`SELECT id, name, interval, expiry, state FROM Timer WHERE user_id=?`, userid)
	if err != nil {
		log.Fatal(err)
	}
	for rows.Next() {
		var t Timer
		t.UserId = userid
		if err := rows.Scan(&t.Id, &t.Name, &t.Interval, &t.Expiry, &t.State); err != nil {
			log.Fatal(err)
		}
		x, err := json.Marshal(t)

		if err != nil {
			panic(err)
		}

		if len(s) > 0 {
			s += ",\n"
		}
		s += "  " + string(x)
	}
	return "[" + s + "]"
}

// Timer entries
func (t *Timer) Create() error {
	res, err := t.Database.db.Exec(
		`INSERT INTO Timer (user_id, name, interval, expiry, state) 
		VALUES (?, ?, ?, ?, ?)`,
		t.UserId,
		t.Name,
		t.Interval,
		t.Expiry,
		t.State,
	)

	if err != nil {
		panic(err)
	}

	t.Id, err = res.LastInsertId()
	if err != nil {
		panic(err)
	}

	log.Println("Timer.Create", t)
	tgid, _ := t.Database.GetUserTelegramIdById(t.UserId)
	msg := fmt.Sprintf("Timer '%s' created", t.Name)
	SendTelegramMsg(tgid, msg)

	return nil
}

func (t *Timer) Delete() (err error) {
	res, err := t.Database.db.Exec(`DELETE FROM Timer WHERE id=? AND user_id=?`, t.Id, t.UserId)
	if err != nil {
		panic(err)
	}

	numDeleted, err := res.RowsAffected()
	if err != nil {
		panic(err)
	}

	if numDeleted != 1 {
		return sql.ErrNoRows
	}

	log.Println("Timer.Delete", t)

	tgid, _ := t.Database.GetUserTelegramIdById(t.UserId)
	msg := fmt.Sprintf("Timer '%s' deleted", t.Name)
	SendTelegramMsg(tgid, msg)

	return nil
}

func (t *Timer) Kick() error {
	now := time.Now().Unix()
	_, err := t.Database.db.Exec(
		`UPDATE Timer 
		SET expiry=interval+?, state="running"
		WHERE id=? and user_id=?`,
		now,
		t.Id,
		t.UserId,
	)

	if err != nil {
		panic(err)
	}

	log.Println("Timer.Kick", t)

	if t.State == "expired" {
		tgid, _ := t.Database.GetUserTelegramIdById(t.UserId)
		msg := fmt.Sprintf("Expired timer '%s' kicked", t.Name)
		SendTelegramMsg(tgid, msg)
	}

	return nil
}

func (t *Timer) Expire() {
	log.Println("Timer.Expire", t)
	if _, err := t.Database.db.Exec(`UPDATE Timer SET state="expired" WHERE id=? AND expiry=?`, t.Id, t.Expiry); err != nil {
		log.Fatal(err)
	}

	tgid, _ := t.Database.GetUserTelegramIdById(t.UserId)
	msg := fmt.Sprintf("Timer '%s' has expired", t.Name)
	SendTelegramMsg(tgid, msg)
}

func (p *Database) ProcessExpiredTimers() int {
	now := time.Now().Unix()
	s := make([]*Timer, 0, 1000)

	// Collect all expired timers
	rows, err := p.db.Query(`SELECT id,user_id,name,expiry FROM Timer WHERE state="running" AND expiry<? LIMIT ?`, now, cap(s))
	if err != nil {
		log.Fatal(err)
	}
	for rows.Next() {
		t := p.NewTimer()
		if err := rows.Scan(&t.Id, &t.UserId, &t.Name, &t.Expiry); err != nil {
			log.Fatal(err)
		}
		s = append(s, t)
	}
	rows.Close()

	// Make them expired
	for _, t := range s {
		t.Expire()
	}

	return len(s)
}
