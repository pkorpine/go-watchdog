package main_test

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pkorpine/go-watchdog/internal/lib"
)

var a lib.App
var testUser = lib.User{
	Name: "TestUser",
	TgId: 123,
}
var cookies []*http.Cookie

func TestMain(m *testing.M) {
	db := "sqlite_test.db"
	a = lib.App{}
	os.Remove(db)

	// Disable telegram initialization
	lib.InitTelegram = func(string, *lib.Database) {
		log.Println("Mock InitTelegram")
	}
	lib.StartTelegram = func() {
		log.Println("Mock StartTelegram")
	}

	a.Initialize(db, "", "")

	// Fill database
	created := a.DB.CreateOrGetUserKeyByTelegramId(&testUser)
	if !created {
		panic("Failed to create user")
	}

	// Get login cookie
	cookies = getCookies()

	// Run tests
	code := m.Run()

	// Cleanup
	a.Exit()
	os.Exit(code)
}

func getTimerList(t *testing.T) []lib.Timer {
	req, _ := http.NewRequest("GET", "/api/timer", nil)
	req.AddCookie(cookies[0])
	rsp := executeRequest(req)
	checkResponseCode(t, http.StatusOK, rsp.Code)
	timers := []lib.Timer{}
	j := json.NewDecoder(rsp.Body).Decode(&timers)
	log.Println("Y", timers)
	if j != nil {
		t.Error("JSON fail")
	}
	return timers
}

func getTimer(t *testing.T, timer lib.Timer) lib.Timer {
	url := fmt.Sprintf("/api/timer/%d", timer.Id)
	req, _ := http.NewRequest("GET", url, nil)
	req.AddCookie(cookies[0])
	rsp := executeRequest(req)
	checkResponseCode(t, http.StatusOK, rsp.Code)
	timerRsp := lib.Timer{}
	j := json.NewDecoder(rsp.Body).Decode(&timerRsp)
	if j != nil {
		t.Error("JSON fail")
	}
	return timerRsp
}

func kickTimer(t *testing.T, timer lib.Timer) {
	url := fmt.Sprintf("/api/timer/%d/kick", timer.Id)
	req, _ := http.NewRequest("GET", url, nil)
	req.AddCookie(cookies[0])
	rsp := executeRequest(req)
	checkResponseCode(t, http.StatusOK, rsp.Code)
}

func kickTimerWithToken(t *testing.T, timer lib.Timer, token string) {
	url := "/kick/" + token
	req, _ := http.NewRequest("GET", url, nil)
	rsp := executeRequest(req)
	checkResponseCode(t, http.StatusOK, rsp.Code)
}

func getTimerToken(t *testing.T, timer lib.Timer) string {
	url := fmt.Sprintf("/api/timer/%d/token", timer.Id)
	req, _ := http.NewRequest("GET", url, nil)
	req.AddCookie(cookies[0])
	rsp := executeRequest(req)
	checkResponseCode(t, http.StatusOK, rsp.Code)
	token, err := rsp.Body.ReadString(byte('\n'))
	if err != io.EOF {
		t.Error("ReadString fail", token, err)
	}
	return token
}

func addTimer(t *testing.T, name string, interval int64) lib.Timer {
	lib.SendTelegramMsg = func(tgid int64, msg string) {
		if tgid != testUser.TgId {
			t.Error("SendTelegramMsg - incorrect tgid")
		}

		if msg != fmt.Sprintf("Timer '%s' created", name) {
			t.Error("SendTelegramMsg - inval")
		}
	}
	p := fmt.Sprintf(`{"name": "%s", "interval": %d}`, name, interval)
	req, _ := http.NewRequest("POST", "/api/timer", strings.NewReader(p))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookies[0])
	rsp := executeRequest(req)
	checkResponseCode(t, http.StatusOK, rsp.Code)
	timer := lib.Timer{}
	j := json.NewDecoder(rsp.Body).Decode(&timer)
	if j != nil {
		t.Error("JSON fail")
	}
	return timer
}

func deleteTimer(t *testing.T, timer lib.Timer, exists bool) {
	lib.SendTelegramMsg = func(tgid int64, msg string) {
		if tgid != testUser.TgId {
			t.Error("SendTelegramMsg - incorrect tgid")
		}

		if msg != fmt.Sprintf("Timer '%s' deleted", timer.Name) {
			t.Error("SendTelegramMsg - inval")
		}
	}
	url := fmt.Sprintf("/api/timer/%d", timer.Id)
	req, _ := http.NewRequest("DELETE", url, nil)
	req.AddCookie(cookies[0])
	rsp := executeRequest(req)
	if exists {
		checkResponseCode(t, http.StatusOK, rsp.Code)
	} else {
		checkResponseCode(t, http.StatusNotFound, rsp.Code)
	}
}

func TestAPI(t *testing.T) {
	const INTERVAL int = 1
	var timers []lib.Timer
	// Get list of timers - empty
	timers = getTimerList(t)
	if len(timers) != 0 {
		t.Error("Timer list expected to be empty")
	}

	// Add timer
	timer1 := addTimer(t, "Test1", int64(INTERVAL))
	if timer1.Name != "Test1" {
		t.Error("New timer - incorrect name")
	}
	if timer1.Interval != int64(INTERVAL) {
		t.Error("New timer - incorrect interval")
	}
	if timer1.State != "new" {
		t.Error("New timer - incorrect state")
	}

	// Get list of timers - 1 timer
	timers = getTimerList(t)
	if len(timers) != 1 {
		t.Error("Timer list expected to be 1")
		t.FailNow()
	}
	if timers[0] != timer1 {
		t.Error("Incorrect timer in list")
	}

	// Kick timer
	i := time.Second * time.Duration(INTERVAL)
	t1 := time.Now().Add(i)
	kickTimer(t, timer1)
	t2 := time.Now().Add(i)

	// Get timer (running)
	timer1b := getTimer(t, timer1)
	if timer1b.State != "running" {
		t.Error("Timer not running after kick")
		t.FailNow()
	}
	if timer1b.Expiry < t1.Unix() || timer1b.Expiry > t2.Unix() {
		t.Error("Timer's expiry set incorrectly", timer1b.Expiry, t1.Unix(), t2.Unix())
		t.FailNow()
	}

	// Wait and process expired timers
	lib.SendTelegramMsg = func(tgid int64, msg string) {
		if tgid != testUser.TgId {
			t.Error("SendTelegramMsg - incorrect tgid")
		}

		if msg != fmt.Sprintf("Timer '%s' has expired", timer1.Name) {
			t.Error("SendTelegramMsg - inval", msg)
		}
	}

	time.Sleep(time.Second * time.Duration(INTERVAL+1))
	a.DB.ProcessExpiredTimers()

	// Get timer (expired)
	timer1c := getTimer(t, timer1)
	if timer1c.State != "expired" {
		t.Error("Timer not expired")
		t.FailNow()
	}

	// Get token
	token := getTimerToken(t, timer1)

	// Kick expired timer using JWT
	lib.SendTelegramMsg = func(tgid int64, msg string) {
		if tgid != testUser.TgId {
			t.Error("SendTelegramMsg - incorrect tgid")
		}

		if msg != fmt.Sprintf("Expired timer '%s' kicked", timer1.Name) {
			t.Error("SendTelegramMsg - inval", msg)
		}
	}
	kickTimerWithToken(t, timer1, token)

	// Get timer (running)
	timer1d := getTimer(t, timer1)
	if timer1d.State != "running" {
		t.Error("Timer not running")
		t.FailNow()
	}

	// Delete timer
	deleteTimer(t, timer1, true)

	// Get list of timers - empty
	timers = getTimerList(t)
	if len(timers) != 0 {
		t.Error("Timer list expected to be empty")
	}

	// Delete timer - should fail
	deleteTimer(t, timer1, false)
}

func TestAPIWithoutCookie(t *testing.T) {
	req, _ := http.NewRequest("GET", "/api/timer", nil)
	response := executeRequest(req)

	checkResponseCode(t, http.StatusBadRequest, response.Code)
}

func TestLogin(t *testing.T) {
	// Correct login
	form := url.Values{}
	form.Add("key", testUser.Key)
	req, _ := http.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	rsp := executeRequest(req)
	checkResponseCode(t, http.StatusOK, rsp.Code)
}

func TestLoginFail(t *testing.T) {
	// Incorrect login
	form := url.Values{}
	form.Add("key", testUser.Key+"x")
	req, _ := http.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	rsp := executeRequest(req)
	checkResponseCode(t, http.StatusUnauthorized, rsp.Code)
}

func getCookies() []*http.Cookie {
	form := url.Values{}
	form.Add("key", testUser.Key)
	req, _ := http.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	rsp := executeRequest(req)
	if rsp.Code != http.StatusOK {
		panic("failed to login")
	}
	return rsp.Result().Cookies()
}

func executeRequest(req *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	a.Rest.ServeHTTP(rr, req)
	return rr
}

func checkResponseCode(t *testing.T, expected, actual int) {
	if expected != actual {
		t.Errorf("Expected response code %d. Got %d\n", expected, actual)
	}
}

func mockTelegram(t *testing.T, tgidExpected int64) {
	lib.SendTelegramMsg = func(tgid int64, msg string) {
		if tgid != tgidExpected {
			t.Errorf("SendTelegramMsg - Expected id %d, got %d\n", tgidExpected, tgid)
		}
	}
}
