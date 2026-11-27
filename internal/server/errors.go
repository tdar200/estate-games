package server

import (
	"errors"
	"net/http"
)

// httpError is an error with an HTTP status. Plain errors (engine errors,
// like JS throws without statusCode) are reported as 400.
type httpError struct {
	status int
	msg    string
}

func (e *httpError) Error() string { return e.msg }

func badRequest(msg string) error { return &httpError{status: http.StatusBadRequest, msg: msg} }
func forbidden(msg string) error  { return &httpError{status: http.StatusForbidden, msg: msg} }
func notFound(msg string) error   { return &httpError{status: http.StatusNotFound, msg: msg} }
func unauthorized(msg string) error {
	return &httpError{status: http.StatusUnauthorized, msg: msg}
}
func tooMany(msg string) error { return &httpError{status: http.StatusTooManyRequests, msg: msg} }

var (
	errInvalidJSON  = &httpError{status: http.StatusBadRequest, msg: "Invalid JSON"}
	errBodyTooLarge = &httpError{status: http.StatusRequestEntityTooLarge, msg: "Request body is too large"}
	errAPINotFound  = &httpError{status: http.StatusNotFound, msg: "API endpoint not found"}

	// Messages shared with src/rooms.js.
	msgRoomMissing      = "The room does not exist or the server restarted"
	msgSessionInvalid   = "Your session is invalid; please join the room again"
	msgSpectatorsCannot = "Spectators cannot perform game actions"
	msgOnlyHost         = "Only the host can perform this action"
	msgRoomLimit        = "The server has reached its room limit; please try again later"
	msgSpectatorLimit   = "This room has reached its spectator limit"
	msgAlreadyStarted   = "The game has already started; joining in progress is not supported"
	msgLobbyOnlyRole    = "Participation can only be changed before the game starts"
	msgInvalidRole      = "Invalid participation role"
	msgCodeExhausted    = "Unable to create a room right now; please try again"

	// Party-mode messages.
	msgDeviceInvalid = "This device is not registered; please reload the page"
	msgDeviceNeeded  = "This request needs a registered device"
	msgNoSeat        = "This device has no seat in this room"
	msgTableOnly     = "Only the table can add local seats"
	msgSeatLobby     = "Seats can only be added before the game starts"
	msgSeatInput     = "Choose a controller or the keyboard for this seat"
	msgSeatMissing   = "That seat is not at this table"
	msgPadTaken      = "That controller already has a seat"
	msgTableNoSeat   = "Choose a seat at the table to act for"
	msgTableRole     = "The table cannot change participation"
)

func statusOf(err error) int {
	var he *httpError
	if errors.As(err, &he) {
		return he.status
	}
	return http.StatusBadRequest
}
