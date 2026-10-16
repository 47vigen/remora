package api

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/47vigen/remora/internal/accounts"
	"github.com/47vigen/remora/internal/api/gen"
)

// toGenAccount maps an account to its answer; quota and models are never null.
func toGenAccount(a Account) gen.Account {
	out := gen.Account{
		Id: a.ID, Provider: a.Provider, Enabled: a.Enabled, Status: a.Status, CooldownUntil: a.CooldownUntil,
		Quota: make([]gen.AccountQuota, 0, len(a.Quota)), Models: append([]string{}, a.Models...),
	}
	if a.Email != "" {
		out.Email = &a.Email
	}
	if a.Message != "" {
		out.Message = &a.Message
	}
	for _, q := range a.Quota {
		g := gen.AccountQuota{RemainingFraction: q.RemainingFraction, ResetAt: q.ResetAt}
		if q.Model != "" {
			g.Model = &q.Model
		}
		out.Quota = append(out.Quota, g)
	}
	return out
}

// ListAccounts lists subscription accounts of one provider or all, searched by email, provider or status, one page
// at a time.
func (s *Server) ListAccounts(ctx context.Context, req gen.ListAccountsRequestObject) (gen.ListAccountsResponseObject, error) {
	if s.d.Accounts == nil {
		return nil, errNotImplemented
	}
	all, err := s.d.Accounts.List(ctx, deref(req.Params.Provider))
	if err != nil {
		return nil, err
	}
	var out []gen.Account
	for _, a := range all {
		if matches(req.Params.Q, a.Email, a.Provider, a.Status) {
			out = append(out, toGenAccount(a))
		}
	}
	items, total := page(out, req.Params.Limit, req.Params.Offset)
	return gen.ListAccounts200JSONResponse{Items: items, Total: total}, nil
}

// GetAccountPool reports, per subscription provider, the enabled accounts that dropped out of the pool.
func (s *Server) GetAccountPool(ctx context.Context, _ gen.GetAccountPoolRequestObject) (gen.GetAccountPoolResponseObject, error) {
	if s.d.Accounts == nil {
		return nil, errNotImplemented
	}
	pool, err := s.d.Accounts.Pool(ctx)
	if err != nil {
		return nil, err
	}
	resp := gen.GetAccountPool200JSONResponse{}
	for _, p := range pool {
		out := make([]gen.PoolAccount, 0, len(p.Out))
		for _, a := range p.Out {
			out = append(out, gen.PoolAccount{Id: strconv.FormatInt(a.ID, 10), Label: a.Label, Reason: a.Reason})
		}
		resp = append(resp, gen.AccountPool{Provider: p.Provider, Enabled: p.Enabled, Out: out})
	}
	return resp, nil
}

// StartAccountLogin starts an OAuth login: a sign-in page to open, or with user_code a device code to enter there.
func (s *Server) StartAccountLogin(ctx context.Context, req gen.StartAccountLoginRequestObject) (gen.StartAccountLoginResponseObject, error) {
	if s.d.Accounts == nil {
		return nil, errNotImplemented
	}
	if req.Body == nil || strings.TrimSpace(req.Body.Provider) == "" {
		return nil, badRequest("a login needs a provider")
	}
	l, err := s.d.Accounts.StartLogin(ctx, strings.TrimSpace(req.Body.Provider))
	if err != nil {
		return nil, err
	}
	out := gen.StartAccountLogin201JSONResponse{State: l.State, Url: l.URL}
	if l.UserCode != "" {
		out.UserCode = &l.UserCode
	}
	return out, nil
}

// GetAccountLogin reports a login's progress.
func (s *Server) GetAccountLogin(ctx context.Context, req gen.GetAccountLoginRequestObject) (gen.GetAccountLoginResponseObject, error) {
	if s.d.Accounts == nil {
		return nil, errNotImplemented
	}
	st, err := s.d.Accounts.LoginStatus(ctx, req.State)
	if err != nil {
		return nil, err
	}
	out := gen.GetAccountLogin200JSONResponse{Status: gen.AccountLoginStatusStatus(st.Status)}
	if st.Message != "" {
		out.Message = &st.Message
	}
	return out, nil
}

// CompleteAccountLogin hands over the address the browser ended on after signing in. The address must be absolute
// and carry a code or an error; its state is optional, and the accounts service checks it against the login when
// it is there.
func (s *Server) CompleteAccountLogin(ctx context.Context, req gen.CompleteAccountLoginRequestObject) (gen.CompleteAccountLoginResponseObject, error) {
	if s.d.Accounts == nil {
		return nil, errNotImplemented
	}
	if req.Body == nil {
		return nil, badRequest("a redirect_url is required")
	}
	raw := strings.TrimSpace(req.Body.RedirectUrl)
	if !validRedirectURL(raw) {
		return nil, badRequest("redirect_url must be the full address the browser ended on, with a code or an error")
	}
	if err := s.d.Accounts.CompleteLogin(ctx, req.State, raw); err != nil {
		return nil, err
	}
	return gen.CompleteAccountLogin202Response{}, nil
}

// validRedirectURL reports whether raw is an absolute URL whose query has a code or an error.
func validRedirectURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}
	q := u.Query()
	return q.Get("code") != "" || q.Get("error") != ""
}

// CancelAccountLogin cancels a login that has not finished.
func (s *Server) CancelAccountLogin(ctx context.Context, req gen.CancelAccountLoginRequestObject) (gen.CancelAccountLoginResponseObject, error) {
	if s.d.Accounts == nil {
		return nil, errNotImplemented
	}
	if err := s.d.Accounts.CancelLogin(ctx, req.State); err != nil {
		return nil, err
	}
	return gen.CancelAccountLogin204Response{}, nil
}

// UpdateAccount enables or disables an account and answers it as it now is.
func (s *Server) UpdateAccount(ctx context.Context, req gen.UpdateAccountRequestObject) (gen.UpdateAccountResponseObject, error) {
	if s.d.Accounts == nil {
		return nil, errNotImplemented
	}
	if req.Body == nil {
		return nil, badRequest("an account update body is required")
	}
	if err := s.d.Accounts.SetEnabled(ctx, req.Id, req.Body.Enabled); err != nil {
		return nil, err
	}
	all, err := s.d.Accounts.List(ctx, "")
	if err != nil {
		return nil, err
	}
	for _, a := range all {
		if a.ID == req.Id {
			return gen.UpdateAccount200JSONResponse(toGenAccount(a)), nil
		}
	}
	return nil, accounts.ErrNotFound
}

// DeleteAccount deletes an account and its sign-in.
func (s *Server) DeleteAccount(ctx context.Context, req gen.DeleteAccountRequestObject) (gen.DeleteAccountResponseObject, error) {
	if s.d.Accounts == nil {
		return nil, errNotImplemented
	}
	if err := s.d.Accounts.Delete(ctx, req.Id); err != nil {
		return nil, err
	}
	return gen.DeleteAccount204Response{}, nil
}

// ResetAccountCooldown clears an account's cooldown.
func (s *Server) ResetAccountCooldown(ctx context.Context, req gen.ResetAccountCooldownRequestObject) (gen.ResetAccountCooldownResponseObject, error) {
	if s.d.Accounts == nil {
		return nil, errNotImplemented
	}
	if err := s.d.Accounts.ResetCooldown(ctx, req.Id); err != nil {
		return nil, err
	}
	return gen.ResetAccountCooldown204Response{}, nil
}

// ImportAccount adds an account from an existing OAuth credential file. The accounts service checks that the
// credential belongs to the provider; the credential itself is never answered.
func (s *Server) ImportAccount(ctx context.Context, req gen.ImportAccountRequestObject) (gen.ImportAccountResponseObject, error) {
	if s.d.Accounts == nil {
		return nil, errNotImplemented
	}
	b := req.Body
	if b == nil || strings.TrimSpace(b.Provider) == "" {
		return nil, badRequest("an import needs a provider")
	}
	if len(b.Credential) == 0 {
		return nil, badRequest("an import needs the credential file's JSON object")
	}
	if t, ok := b.Credential["type"]; ok {
		if _, isString := t.(string); !isString {
			return nil, badRequest("the credential's type must be a string")
		}
	}
	data, err := json.Marshal(b.Credential)
	if err != nil {
		return nil, badRequest("the credential is not valid JSON")
	}
	a, err := s.d.Accounts.Import(ctx, strings.TrimSpace(b.Provider), data)
	if err != nil {
		return nil, err
	}
	return gen.ImportAccount201JSONResponse(toGenAccount(a)), nil
}
