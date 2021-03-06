package messages

import (
	"errors"
	"fmt"
	"github.com/jcmturner/asn1"
	"github.com/jcmturner/gokrb5/iana/asnAppTag"
	"github.com/jcmturner/gokrb5/iana/msgtype"
	"github.com/jcmturner/gokrb5/types"
	"github.com/jcmturner/gokrb5/iana"
	"github.com/jcmturner/gokrb5/crypto"
	"github.com/jcmturner/gokrb5/asn1tools"
	"github.com/jcmturner/gokrb5/iana/keyusage"
)

/*AP-REQ          ::= [APPLICATION 14] SEQUENCE {
pvno            [0] INTEGER (5),
msg-type        [1] INTEGER (14),
ap-options      [2] APOptions,
ticket          [3] Ticket,
authenticator   [4] EncryptedData -- Authenticator
}

APOptions       ::= KerberosFlags
-- reserved(0),
-- use-session-key(1),
-- mutual-required(2)*/

type marshalAPReq struct {
	PVNO      int            `asn1:"explicit,tag:0"`
	MsgType   int            `asn1:"explicit,tag:1"`
	APOptions asn1.BitString `asn1:"explicit,tag:2"`
	// Ticket needs to be a raw value as it is wrapped in an APPLICATION tag
	Ticket        asn1.RawValue       `asn1:"explicit,tag:3"`
	Authenticator types.EncryptedData `asn1:"explicit,tag:4"`
}

type APReq struct {
	PVNO          int                 `asn1:"explicit,tag:0"`
	MsgType       int                 `asn1:"explicit,tag:1"`
	APOptions     asn1.BitString      `asn1:"explicit,tag:2"`
	Ticket        types.Ticket        `asn1:"explicit,tag:3"`
	Authenticator types.EncryptedData `asn1:"explicit,tag:4"`
}

func NewAPReq(TGT types.Ticket, sessionKey types.EncryptionKey, auth types.Authenticator) (APReq, error) {
	var a APReq
	ed, err := encryptAuthenticator(auth, sessionKey)
	if err != nil {
		return a, fmt.Errorf("Error creating authenticator for AP_REQ: %v", err)
	}
	a = APReq{
		PVNO:    iana.PVNO,
		MsgType: msgtype.KRB_AP_REQ,
		APOptions: types.NewKrbFlags(),
		Ticket: TGT,
		Authenticator: ed,
	}
	return a, nil
}

func encryptAuthenticator(a types.Authenticator, sessionKey types.EncryptionKey) (types.EncryptedData, error) {
	var ed types.EncryptedData
	m, err := a.Marshal()
	if err != nil {
		return ed, fmt.Errorf("Error marshalling authenticator: %v", err)
	}
	return crypto.GetEncryptedData(m, sessionKey, keyusage.TGS_REQ_PA_TGS_REQ_AP_REQ_AUTHENTICATOR, 0)
}

func (a *APReq) Unmarshal(b []byte) error {
	var m marshalAPReq
	_, err := asn1.UnmarshalWithParams(b, &m, fmt.Sprintf("application,explicit,tag:%v", asnAppTag.APREQ))
	if err != nil {
		return err
	}
	if m.MsgType != msgtype.KRB_AP_REQ {
		return errors.New("Message ID does not indicate a KRB_AS_REP")
	}
	a.PVNO = m.PVNO
	a.MsgType = m.MsgType
	a.APOptions = m.APOptions
	a.Authenticator = m.Authenticator
	a.Ticket, err = types.UnmarshalTicket(m.Ticket.Bytes)
	if err != nil {
		return fmt.Errorf("Error unmarshalling ticket in AP_REQ; %v", err)
	}
	return nil
}

func (a *APReq) Marshal() ([]byte, error) {
	m := marshalAPReq{
		PVNO:    a.PVNO,
		MsgType: a.MsgType,
		APOptions: a.APOptions,
		Authenticator: a.Authenticator,
	}
	var b []byte
	b, err := a.Ticket.Marshal()
	if err != nil {
		return b, err
	}
	m.Ticket = asn1.RawValue{
		Class:      2,
		IsCompound: true,
		Tag:        3,
		Bytes:      b,
	}
	mk, err := asn1.Marshal(m)
	if err != nil {
		return mk, fmt.Errorf("Error marshalling AP_REQ: %v", err)
	}
	mk = asn1tools.AddASNAppTag(mk, asnAppTag.APREQ)
	return mk, nil
}