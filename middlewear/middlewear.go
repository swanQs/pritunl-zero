package middlewear

import (
	"fmt"
	"github.com/Sirupsen/logrus"
	"github.com/dropbox/godropbox/errors"
	"github.com/gin-gonic/gin"
	"github.com/pritunl/pritunl-zero/auth"
	"github.com/pritunl/pritunl-zero/authorizer"
	"github.com/pritunl/pritunl-zero/csrf"
	"github.com/pritunl/pritunl-zero/database"
	"github.com/pritunl/pritunl-zero/errortypes"
	"github.com/pritunl/pritunl-zero/node"
	"github.com/pritunl/pritunl-zero/service"
	"github.com/pritunl/pritunl-zero/session"
	"github.com/pritunl/pritunl-zero/utils"
	"github.com/pritunl/pritunl-zero/validator"
	"net/http"
)

const robots = `User-agent: *
Disallow: /
`

func Limiter(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1000000)
}

func Counter(c *gin.Context) {
	node.Self.AddRequest()
}

func Database(c *gin.Context) {
	db := database.GetDatabase()
	c.Set("db", db)
	c.Next()
	db.Close()
}

func Session(c *gin.Context) {
	db := c.MustGet("db").(*database.Database)

	authr, err := authorizer.Authorize(db, c.Writer, c.Request)
	if err != nil {
		switch err.(type) {
		case *errortypes.AuthenticationError:
			utils.AbortWithError(c, 401, err)
			break
		default:
			utils.AbortWithError(c, 500, err)
		}
		return
	}

	if authr.IsValid() {
		usr, err := authr.GetUser(db)
		if err != nil {
			utils.AbortWithError(c, 500, err)
			return
		}

		if usr != nil {
			active, err := auth.SyncUser(db, usr)
			if err != nil {
				utils.AbortWithError(c, 500, err)
				return
			}

			if !active {
				err = authr.Clear(db, c.Writer, c.Request)
				if err != nil {
					utils.AbortWithError(c, 500, err)
					return
				}

				err = session.RemoveAll(db, usr.Id)
				if err != nil {
					utils.AbortWithError(c, 500, err)
					return
				}
			}
		}
	}

	c.Set("authorizer", authr)
}

func SessionProxy(c *gin.Context) {
	db := c.MustGet("db").(*database.Database)
	srvc := c.MustGet("service").(*service.Service)

	if srvc == nil {
		authr := authorizer.NewProxy()
		c.Set("authorizer", authr)
		return
	}

	authr, err := authorizer.AuthorizeProxy(db, srvc, c.Writer, c.Request)
	if err != nil {
		utils.AbortWithError(c, 500, err)
		return
	}

	if authr.IsValid() {
		usr, err := authr.GetUser(db)
		if err != nil {
			utils.AbortWithError(c, 500, err)
			return
		}

		if usr != nil {
			active, err := auth.SyncUser(db, usr)
			if err != nil {
				utils.AbortWithError(c, 500, err)
				return
			}

			if !active {
				err = authr.Clear(db, c.Writer, c.Request)
				if err != nil {
					utils.AbortWithError(c, 500, err)
					return
				}

				err = session.RemoveAll(db, usr.Id)
				if err != nil {
					return
				}
			}
		}
	}

	c.Set("authorizer", authr)
}

func Auth(c *gin.Context) {
	db := c.MustGet("db").(*database.Database)
	authr := c.MustGet("authorizer").(*authorizer.Authorizer)

	if !authr.IsValid() {
		utils.AbortWithStatus(c, 401)
		return
	}

	usr, err := authr.GetUser(db)
	if err != nil {
		utils.AbortWithError(c, 500, err)
		return
	}

	if usr == nil {
		utils.AbortWithStatus(c, 401)
		return
	}

	errData, err := validator.ValidateAdmin(db, usr)
	if err != nil {
		utils.AbortWithError(c, 500, err)
		return
	}

	if errData != nil {
		err = authr.Clear(db, c.Writer, c.Request)
		if err != nil {
			utils.AbortWithError(c, 500, err)
			return
		}

		utils.AbortWithStatus(c, 401)
		return
	}
}

func CsrfToken(c *gin.Context) {
	db := c.MustGet("db").(*database.Database)
	authr := c.MustGet("authorizer").(*authorizer.Authorizer)

	if !authr.IsValid() {
		utils.AbortWithStatus(c, 401)
		return
	}

	if authr.IsApi() {
		return
	}

	token := ""
	if c.Request.Header.Get("Upgrade") == "websocket" {
		token = c.Query("csrf_token")
	} else {
		token = c.Request.Header.Get("Csrf-Token")
	}

	valid, err := csrf.ValidateToken(db, authr.SessionId(), token)
	if err != nil {
		switch err.(type) {
		case *database.NotFoundError:
			utils.AbortWithStatus(c, 401)
			break
		default:
			utils.AbortWithError(c, 500, err)
		}
		return
	}

	if !valid {
		utils.AbortWithStatus(c, 401)
		return
	}
}

func Recovery(c *gin.Context) {
	defer func() {
		if c.Errors != nil && len(c.Errors) != 0 {
			logrus.WithFields(logrus.Fields{
				"client": node.Self.GetRemoteAddr(c.Request),
				"error":  c.Errors,
			}).Error("middlewear: Handler error")
		}

		if r := recover(); r != nil {
			logrus.WithFields(logrus.Fields{
				"client": node.Self.GetRemoteAddr(c.Request),
				"error":  errors.New(fmt.Sprintf("%s", r)),
			}).Error("middlewear: Handler panic")
			utils.AbortWithStatus(c, 500)
			return
		}
	}()

	c.Next()
}

func RobotsGet(c *gin.Context) {
	c.String(200, robots)
}

func NotFound(c *gin.Context) {
	utils.AbortWithStatus(c, 404)
}
