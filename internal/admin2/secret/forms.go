package secret

import "net/http"

type UnlockForm struct {
	Passphrase string
}

func DecodeUnlockForm(r *http.Request) (UnlockForm, error) {
	if err := r.ParseForm(); err != nil {
		return UnlockForm{}, err
	}
	return UnlockForm{Passphrase: r.FormValue("passphrase")}, nil
}

func (f UnlockForm) ToInput() UnlockInput {
	return UnlockInput{Passphrase: f.Passphrase}
}
