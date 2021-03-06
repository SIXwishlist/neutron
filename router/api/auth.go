package api

import (
	"errors"
	"encoding/json"
	"strings"

	"gopkg.in/macaron.v1"
)

type TokenType string

const (
	TokenBearer TokenType = "Bearer"
)

type AuthReq struct {
	Req
	ClientID string
	ClientSecret string
	Username string
	Password string
	TwoFactorCode string
}

type AuthResp struct {
	Resp
	AccessToken string
	ExpiresIn int
	TokenType TokenType
	Scope string
	Uid string
	RefreshToken string
	UserStatus int
	PrivateKey string
	KeySalt string
	EventID string
}

type AuthCookiesReq struct {
	Req
	ClientID string
	RefreshToken string
	RedirectURI string
	State string
}

type AuthCookiesResp struct {
	Resp
	SessionToken string
}

type AuthCookie struct {
	AccessToken string
	Uid string `json:"UID"`
}

type AuthInfoReq struct {
	Req
	ClientID string
	ClientSecret string
	Username string
}

type AuthInfoResp struct {
	Resp
	Modulus string
	ServerEphemeral string
	Version int
	Salt string
	SRPSession string
	TwoFactor int
}

func (api *Api) Auth(ctx *macaron.Context, req AuthReq) {
	user, err := api.backend.Auth(req.Username, req.Password)
	if err != nil {
		ctx.JSON(200, &ErrorResp{
			Resp: Resp{Unauthorized},
			Error: "invalid_grant",
			ErrorDescription: err.Error(),
		})
		return
	}

	err = api.populateCurrentUser(user)
	if err != nil {
		ctx.JSON(200, newErrorResp(err))
		return
	}

	var session *Session
	session = NewSession(user.ID, func() {
		delete(api.sessions, session.ID)

		// Check if there are remaining sessions for this user
		for _, s := range api.sessions {
			if s.UserID == session.UserID {
				return
			}
		}

		// Stop producing events for this user
		api.backend.DeleteAllEvents(session.UserID)
	})
	api.sessions[session.ID] = session

	addr := user.GetMainAddress()
	if len(addr.Keys) == 0 {
		ctx.JSON(200, newErrorResp(errors.New("User has no private key")))
		return
	}

	kp := addr.Keys[0]
	encryptedToken, err := kp.Encrypt(session.Token)
	if err != nil {
		ctx.JSON(200, &ErrorResp{
			Resp: Resp{InternalServerError},
			Error: "invalid_key",
			ErrorDescription: err.Error(),
		})
		return
	}

	lastEvent, err := api.backend.GetLastEvent(user.ID)
	if err != nil {
		ctx.JSON(500, newErrorResp(err))
		return
	}

	ctx.JSON(200, &AuthResp{
		Resp: Resp{Ok},
		AccessToken: encryptedToken,
		ExpiresIn: 360000, // TODO: really expire
		TokenType: TokenBearer,
		Scope: "full mail payments reset keys",
		Uid: session.ID,
		RefreshToken: "refresh_token", // TODO
		PrivateKey: kp.PrivateKey,
		EventID: lastEvent.ID,
	})
}

func (api *Api) AuthInfo(ctx *macaron.Context, req AuthInfoReq) {
	ctx.JSON(200, &AuthInfoResp{
		Resp: Resp{Ok},
	})
}

func (api *Api) AuthCookies(ctx *macaron.Context, req AuthCookiesReq) {
	uid := api.getUid(ctx)
	if uid == "" {
		ctx.JSON(200, &ErrorResp{
			Resp: Resp{BadRequest},
			Error: "invalid_grant",
			ErrorDescription: "No uid provided",
		})
		return
	}

	session, ok := api.sessions[uid]
	if !ok {
		ctx.JSON(200, &ErrorResp{
			Resp: Resp{BadRequest},
			Error: "invalid_session",
			ErrorDescription: "Invalid UID",
		})
		return
	}

	auth, ok := ctx.Req.Header["Authorization"]
	if !ok || len(auth) == 0 {
		ctx.JSON(200, &ErrorResp{
			Resp: Resp{BadRequest},
			Error: "invalid_authorization",
			ErrorDescription: "Invalid authorization header",
		})
		return
	}

	parts := strings.SplitN(auth[0], " ", 2)
	if len(parts) != 2 {
		ctx.JSON(200, &ErrorResp{
			Resp: Resp{BadRequest},
			Error: "invalid_authorization",
			ErrorDescription: "Invalid authorization header",
		})
		return
	}

	tokenType := parts[0]
	token := parts[1]

	if TokenType(tokenType) != TokenBearer || token != session.Token {
		ctx.JSON(200, &ErrorResp{
			Resp: Resp{BadRequest},
			Error: "invalid_authorization",
			ErrorDescription: "Invalid authorization header",
		})
		return
	}

	authCookie, _ := json.Marshal(&AuthCookie{
		AccessToken: session.Token,
		Uid: session.ID,
	})
	ctx.SetCookie("AUTH-" + session.Token, string(authCookie), 0, "/api/", "", false, true)

	ctx.JSON(200, &AuthCookiesResp{
		Resp: Resp{Ok},
		SessionToken: session.Token,
	})
}

func (api *Api) DeleteAuth(ctx *macaron.Context) {
	sessionToken := api.getSessionToken(ctx)
	if sessionToken != "" {
		ctx.SetCookie("AUTH-" + sessionToken, "", 0, "/api/", "", false, true)

		delete(api.sessions, "sessionToken")
	}

	ctx.JSON(200, &Resp{Ok})
}
