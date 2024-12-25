package lib

import (
	"bytes"
	"fmt"
	"html/template"
	"log"
	"strconv"
	"time"

	"net/http"

	"github.com/dgrijalva/jwt-go"
	"github.com/labstack/echo"
	"github.com/labstack/echo/middleware"
)

func getUser(c echo.Context) int64 {
	user := c.Get("user").(*jwt.Token)
	claims := user.Claims.(jwt.MapClaims)
	userid := int64(claims["userid"].(float64))
	return userid
}

func getTimer(c echo.Context, db *Database) *Timer {
	var id, userid int64
	var err error
	id, err = strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		panic(err)
	}
	userid = getUser(c)
	t := db.GetTimer(id, userid)
	return t
}

func NewRestServer(prefix string, db *Database, hmacSecret string) (e *echo.Echo) {
	hmacSecretBytes := []byte(hmacSecret)
	e = echo.New()

	e.Pre(middleware.Rewrite(map[string]string{
		prefix + "/api/*":    "/restricted/api/$1",
		prefix + "/":         "/",
		prefix + "/login":    "/login",
		prefix + "/static/*": "/static/$1",
		prefix + "/kick/*":   "/kick/$1",
	}))

	e.Use(middleware.LoggerWithConfig(middleware.LoggerConfig{
		Format: "${time_rfc3339} ${status} ${method} ${uri} ${remote_ip}\n",
	}))
	e.Use(middleware.Recover())

	e.Static("/static", "static")

	// Root HTML
	e.GET("/", func(c echo.Context) error {
		var tmplBuf bytes.Buffer
		tmplData := struct {
			LoginURL string
		}{
			LoginURL: TgLoginURL,
		}
		template.Must(template.ParseFiles("static/main.html")).Execute(&tmplBuf, tmplData)
		return c.HTML(http.StatusOK, tmplBuf.String())
	})

	// Login
	e.POST("/login", func(c echo.Context) error {
		key := c.FormValue("key")
		log.Println("login", key)

		// Find user_id
		userid, err := db.GetUserIdByKey(key)
		if err != nil {
			// Invalid key or no key
			log.Println("Login failed")
			return c.String(http.StatusUnauthorized, "Failed to login\n")
		}

		exp := time.Now().Add(24 * time.Hour)

		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"userid": userid,
			"exp":    exp.Unix(),
		})
		tokenString, err := token.SignedString(hmacSecretBytes)

		if err != nil {
			fmt.Println(err)
			return c.String(http.StatusUnauthorized, "Failed to login\n")
		}

		cookie := new(http.Cookie)
		cookie.Name = "Authorization"
		cookie.Value = tokenString
		cookie.Expires = exp
		c.SetCookie(cookie)

		//return c.String(http.StatusMovedPermanently, "/")
		return c.String(http.StatusOK, "Login OK\n")
	})

	// Restricted group
	g := e.Group("/restricted")

	g.Use(middleware.JWTWithConfig(middleware.JWTConfig{
		SigningKey:  hmacSecretBytes,
		TokenLookup: "cookie:Authorization",
	}))

	// Create timer
	g.POST("/api/timer", func(c echo.Context) error {
		var err error
		type RestTimerParams struct {
			Name     string `json:"name" form:"name" query:"name"`
			Interval int    `json:"interval" form:"interval" query:"interval"`
		}
		rt := Timer{}

		if err := c.Bind(&rt); err != nil {
			log.Println("POST /api/timer - bind error", err)
			return err
		}

		t := db.NewTimer()
		t.Name = rt.Name
		t.Interval = rt.Interval
		t.UserId = getUser(c)

		err = t.Create()

		if err != nil {
			return c.String(http.StatusInternalServerError, "Failed to create timer")
		}

		return c.JSON(http.StatusOK, t)
	})

	// Get list of timers
	g.GET("/api/timer", func(c echo.Context) error {
		userid := getUser(c)
		msg := db.GetTimersJSON(userid)
		return c.String(http.StatusOK, msg)
	})

	// Delete timer
	g.DELETE("/api/timer/:id", func(c echo.Context) error {
		t := getTimer(c, db)
		if t == nil {
			return c.String(http.StatusNotFound, "Timer not found")
		}

		t.Delete()

		return c.String(http.StatusOK, "Timer deleted")
	})

	// Get timer status
	g.GET("/api/timer/:id", func(c echo.Context) error {
		t := getTimer(c, db)
		if t == nil {
			return c.String(http.StatusNotFound, "Timer not found")
		}

		return c.JSON(http.StatusOK, t)
	})

	// Get timer JWT
	g.GET("/api/timer/:id/token", func(c echo.Context) error {
		t := getTimer(c, db)
		if t == nil {
			return c.String(http.StatusNotFound, "Timer not found")
		}

		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"userid":  t.UserId,
			"timerid": t.Id,
		})
		tokenString, err := token.SignedString(hmacSecretBytes)

		if err != nil {
			fmt.Println(err)
		}

		return c.String(http.StatusOK, tokenString)
	})

	// Kick timer
	g.GET("/api/timer/:id/kick", func(c echo.Context) error {
		t := getTimer(c, db)
		if t == nil {
			return c.String(http.StatusNotFound, "Timer not found")
		}

		t.Kick()
		return c.String(http.StatusOK, "Timer kicked")
	})

	// Modify timer
	// e.PUT()

	e.GET("/kick/:token", func(c echo.Context) error {
		tokenString := c.Param("token")

		// Validate token and extract TimerId and UserId
		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("Unexpected signing method")
			}
			return hmacSecretBytes, nil
		})

		if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
			timerid := int64(claims["timerid"].(float64))
			userid := int64(claims["userid"].(float64))
			t := db.GetTimer(timerid, userid)
			t.Kick()
			return c.String(http.StatusOK, "Timer kicked")
		} else {
			fmt.Println(err)
			return c.String(http.StatusBadRequest, err.Error())
		}
	})

	return e
}
