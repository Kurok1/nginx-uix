/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package httpapi

import (
	"net/http"
	"net/url"

	"github.com/kuroky/nginx-uix/internal/config"
)

func authorizeBusinessMutation(
	writer http.ResponseWriter,
	request *http.Request,
	sessions SessionService,
	publicURL *url.URL,
) (config.Actor, bool) {
	requestID := requestIDFromContext(request.Context())
	rawToken, ok := readSessionCookie(request)
	if !ok || sessions == nil {
		writeAPIError(writer, requestID, http.StatusUnauthorized, "unauthenticated", "需要登录", nil)
		return config.Actor{}, false
	}
	issued, err := sessions.Current(request.Context(), rawToken)
	if err != nil {
		(&sessionHandler{service: sessions}).writeAuthError(writer, requestID, err)
		return config.Actor{}, false
	}
	if !originMatches(request, publicURL) {
		writeAPIError(writer, requestID, http.StatusForbidden, "origin_rejected", "请求来源不受信任", nil)
		return config.Actor{}, false
	}
	csrfValues := request.Header.Values(csrfHeaderName)
	if len(csrfValues) != 1 || csrfValues[0] == "" {
		writeAPIError(writer, requestID, http.StatusForbidden, "csrf_rejected", "CSRF 校验失败", nil)
		return config.Actor{}, false
	}
	if err := sessions.VerifyCSRF(request.Context(), rawToken, csrfValues[0]); err != nil {
		(&sessionHandler{service: sessions}).writeAuthError(writer, requestID, err)
		return config.Actor{}, false
	}
	return config.Actor{UserID: issued.User.ID, RequestID: requestID}, true
}
