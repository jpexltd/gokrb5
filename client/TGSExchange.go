package client

import (
	"errors"
	"fmt"
	"github.com/jcmturner/gokrb5/iana/nametype"
	"github.com/jcmturner/gokrb5/messages"
	"github.com/jcmturner/gokrb5/types"
	"strings"
)

// Perform a TGS exchange to retrieve a ticket to the specified SPN.
// The ticket retrieved is added to the client's cache.
func (cl *Client) TGSExchange(spn types.PrincipalName, renewal bool) (tgsReq messages.TGSReq, tgsRep messages.TGSRep, err error) {
	if cl.Session == nil {
		return tgsReq, tgsRep, errors.New("Error client does not have a session. Client needs to login first")
	}
	tgsReq, err = messages.NewTGSReq(cl.Credentials.Username, cl.Config, cl.Session.TGT, cl.Session.SessionKey, spn, renewal)
	if err != nil {
		return tgsReq, tgsRep, fmt.Errorf("Error generating New TGS_REQ: %v", err)
	}
	b, err := tgsReq.Marshal()
	if err != nil {
		return tgsReq, tgsRep, fmt.Errorf("Error marshalling TGS_REQ: %v", err)
	}
	r, err := cl.SendToKDC(b)
	if err != nil {
		return tgsReq, tgsRep, fmt.Errorf("Error sending TGS_REQ to KDC: %v", err)
	}
	err = tgsRep.Unmarshal(r)
	if err != nil {
		return tgsReq, tgsRep, fmt.Errorf("Error unmarshalling TGS_REP: %v", err)
	}
	err = tgsRep.DecryptEncPart(cl.Session.SessionKey)
	if err != nil {
		return tgsReq, tgsRep, fmt.Errorf("Error decrypting EncPart of TGS_REP: %v", err)
	}
	if ok, err := tgsRep.IsValid(cl.Config, tgsReq); !ok {
		return tgsReq, tgsRep, fmt.Errorf("TGS_REP is not valid: %v", err)
	}
	return tgsReq, tgsRep, nil
}

// Make a request to get a service ticket for the SPN specified
// SPN format: <SERVICE>/<FQDN> Eg. HTTP/www.example.com
// The ticket will be added to the client's ticket cache
func (cl *Client) GetServiceTicket(spn string) error {
	s := strings.Split(spn, "/")
	princ := types.PrincipalName{
		NameType:   nametype.KRB_NT_PRINCIPAL,
		NameString: s,
	}
	_, tgsRep, err := cl.TGSExchange(princ, false)
	if err != nil {
		return err
	}
	cl.Cache.AddEntry(tgsRep.Ticket, tgsRep.DecryptedEncPart.AuthTime, tgsRep.DecryptedEncPart.EndTime, tgsRep.DecryptedEncPart.RenewTill)
	return nil
}
