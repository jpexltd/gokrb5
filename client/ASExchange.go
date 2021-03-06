package client

import (
	"errors"
	"fmt"
	"github.com/jcmturner/gokrb5/crypto"
	"github.com/jcmturner/gokrb5/iana/errorcode"
	"github.com/jcmturner/gokrb5/iana/patype"
	"github.com/jcmturner/gokrb5/messages"
	"github.com/jcmturner/gokrb5/types"
	"sort"
)

// Login the client with the KDC via an AS exchange.
func (cl *Client) Login() error {
	return cl.ASExchange()
}

// Perform an AS exchange for the client to retrieve a TGT.
func (cl *Client) ASExchange() error {
	if !cl.IsConfigured() {
		return errors.New("Client is not configured correctly.")
	}
	a := messages.NewASReq(cl.Config, cl.Credentials.Username)
	b, err := a.Marshal()
	if err != nil {
		return fmt.Errorf("Error marshalling AS_REQ: %v", err)
	}
	rb, err := cl.SendToKDC(b)
	if err != nil {
		return fmt.Errorf("Error sending AS_REQ to KDC: %v", err)
	}
	var ar messages.ASRep
	err = ar.Unmarshal(rb)
	if err != nil {
		//A KRBError may have been returned instead.
		var krberr messages.KRBError
		err = krberr.Unmarshal(rb)
		if err != nil {
			return fmt.Errorf("Could not unmarshal data returned from KDC: %v", err)
		}
		if krberr.ErrorCode == errorcode.KDC_ERR_PREAUTH_REQUIRED {
			paTSb, err := types.GetPAEncTSEncAsnMarshalled()
			if err != nil {
				return fmt.Errorf("Error creating PAEncTSEnc for Pre-Authentication: %v", err)
			}
			sort.Sort(sort.Reverse(sort.IntSlice(cl.Config.LibDefaults.Default_tkt_enctype_ids)))
			etype, err := crypto.GetEtype(cl.Config.LibDefaults.Default_tkt_enctype_ids[0])
			if err != nil {
				return fmt.Errorf("Error creating etype: %v", err)
			}
			//paEncTS, err := crypto.GetEncryptedData(paTSb, etype, cl.Config.LibDefaults.Default_realm, cl.Credentials.Username, cl.Credentials.Keytab, 1)
			key, err := cl.Credentials.Keytab.GetEncryptionKey(cl.Credentials.Username, cl.Config.LibDefaults.Default_realm, 1, etype.GetETypeID())
			paEncTS, err := crypto.GetEncryptedData(paTSb, key, 0, 1)
			if err != nil {
				return fmt.Errorf("Error encrypting pre-authentication timestamp: %v", err)
			}
			pa := types.PAData{
				PADataType:  patype.PA_ENC_TIMESTAMP,
				PADataValue: paEncTS.Cipher,
			}
			a.PAData = append(a.PAData, pa)
			b, err := a.Marshal()
			if err != nil {
				return fmt.Errorf("Error marshalling AS_REQ: %v", err)
			}
			rb, err := cl.SendToKDC(b)
			if err != nil {
				return fmt.Errorf("Error sending AS_REQ to KDC: %v", err)
			}
			err = ar.Unmarshal(rb)
			if err != nil {
				return fmt.Errorf("Could not unmarshal data returned from KDC: %v", err)
			}
		}
		return krberr
	}
	err = ar.DecryptEncPart(cl.Credentials)
	if err != nil {
		return fmt.Errorf("Error decrypting EncPart of AS_REP: %v", err)
	}
	if ok, err := ar.IsValid(cl.Config, a); !ok {
		return fmt.Errorf("AS_REP is not valid: %v", err)
	}
	cl.Session = &Session{
		AuthTime:             ar.DecryptedEncPart.AuthTime,
		EndTime:              ar.DecryptedEncPart.EndTime,
		RenewTill:            ar.DecryptedEncPart.RenewTill,
		TGT:                  ar.Ticket,
		SessionKey:           ar.DecryptedEncPart.Key,
		SessionKeyExpiration: ar.DecryptedEncPart.KeyExpiration,
	}
	return nil
}
