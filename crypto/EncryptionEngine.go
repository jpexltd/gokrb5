package crypto

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/jcmturner/gokrb5/iana/chksumtype"
	"github.com/jcmturner/gokrb5/iana/etype"
	"github.com/jcmturner/gokrb5/iana/patype"
	"github.com/jcmturner/gokrb5/types"
	"hash"
)

type EType interface {
	GetETypeID() int
	GetHashID() int
	GetKeyByteSize() int                                        // See "protocol key format" for defined values
	GetKeySeedBitLength() int                                   // key-generation seed length, k
	GetDefaultStringToKeyParams() string                        // default string-to-key parameters (s2kparams)
	StringToKey(string, salt, s2kparams string) ([]byte, error) // string-to-key (UTF-8 string, UTF-8 string, opaque)->(protocol-key)
	RandomToKey(b []byte) []byte                                // random-to-key (bitstring[K])->(protocol-key)
	GetHMACBitLength() int                                      // HMAC output size, h
	GetMessageBlockByteSize() int                               // message block size, m
	Encrypt(key, message []byte) ([]byte, []byte, error)        // E function - encrypt (specific-key, state, octet string)->(state, octet string)
	Decrypt(key, ciphertext []byte) ([]byte, error)             // D function
	GetCypherBlockBitLength() int                               // cipher block size, c
	GetConfounderByteSize() int                                 // This is the same as the cipher block size but in bytes.
	DeriveKey(protocolKey, usage []byte) ([]byte, error)        // DK key-derivation (protocol-key, integer)->(specific-key)
	DeriveRandom(protocolKey, usage []byte) ([]byte, error)     // DR pseudo-random (protocol-key, octet-string)->(octet-string)
	VerifyIntegrity(protocolKey, ct, pt []byte, usage uint32) bool
	GetHash() hash.Hash
}

func GetEtype(id int) (EType, error) {
	switch id {
	case etype.AES128_CTS_HMAC_SHA1_96:
		var et Aes128CtsHmacSha96
		return et, nil
	case etype.AES256_CTS_HMAC_SHA1_96:
		var et Aes256CtsHmacSha96
		return et, nil
	default:
		return nil, fmt.Errorf("Unknown or unsupported EType: %d", id)
	}
}

func GetChksumEtype(id int) (EType, error) {
	switch id {
	case chksumtype.HMAC_SHA1_96_AES128:
		var et Aes128CtsHmacSha96
		return et, nil
	case chksumtype.HMAC_SHA1_96_AES256:
		var et Aes256CtsHmacSha96
		return et, nil
	default:
		return nil, fmt.Errorf("Unknown or unsupported checksum type: %d", id)
	}
}

// RFC3961: DR(Key, Constant) = k-truncate(E(Key, Constant, initial-cipher-state))
// key - base key or protocol key. Likely to be a key from a keytab file
// usage - a constant
// n - block size in bits (not bytes) - note if you use something like aes.BlockSize this is in bytes.
// k - key length / key seed length in bits. Eg. for AES256 this value is 256
// encrypt - the encryption function to use
func deriveRandom(key, usage []byte, n, k int, e EType) ([]byte, error) {
	//Ensure the usage constant is at least the size of the cypher block size. Pass it through the nfold algorithm that will "stretch" it if needs be.
	nFoldUsage := Nfold(usage, n)
	//k-truncate implemented by creating a byte array the size of k (k is in bits hence /8)
	out := make([]byte, k/8)

	/*If the output	of E is shorter than k bits, it is fed back into the encryption as many times as necessary.
	The construct is as follows (where | indicates concatentation):

	K1 = E(Key, n-fold(Constant), initial-cipher-state)
	K2 = E(Key, K1, initial-cipher-state)
	K3 = E(Key, K2, initial-cipher-state)
	K4 = ...

	DR(Key, Constant) = k-truncate(K1 | K2 | K3 | K4 ...)*/
	_, K, err := e.Encrypt(key, nFoldUsage)
	if err != nil {
		return out, err
	}
	for i := copy(out, K); i < len(out); {
		_, K, _ = e.Encrypt(key, K)
		i = i + copy(out[i:], K)
	}
	return out, nil
}

func zeroPad(b []byte, m int) ([]byte, error) {
	if m <= 0 {
		return nil, errors.New("Invalid message block size when padding")
	}
	if b == nil || len(b) == 0 {
		return nil, errors.New("Data not valid to pad: Zero size")
	}
	if l := len(b) % m; l != 0 {
		n := m - l
		z := make([]byte, n)
		b = append(b, z...)
	}
	return b, nil
}

func pkcs7Pad(b []byte, m int) ([]byte, error) {
	if m <= 0 {
		return nil, errors.New("Invalid message block size when padding")
	}
	if b == nil || len(b) == 0 {
		return nil, errors.New("Data not valid to pad: Zero size")
	}
	n := m - (len(b) % m)
	pb := make([]byte, len(b)+n)
	copy(pb, b)
	copy(pb[len(b):], bytes.Repeat([]byte{byte(n)}, n))
	return pb, nil
}

func pkcs7Unpad(b []byte, m int) ([]byte, error) {
	if m <= 0 {
		return nil, errors.New("Invalid message block size when unpadding")
	}
	if b == nil || len(b) == 0 {
		return nil, errors.New("Padded data not valid: Zero size")
	}
	if len(b)%m != 0 {
		return nil, errors.New("Padded data not valid: Not multiple of message block size")
	}
	c := b[len(b)-1]
	n := int(c)
	if n == 0 || n > len(b) {
		return nil, errors.New("Padded data not valid: Data may not have been padded")
	}
	for i := 0; i < n; i++ {
		if b[len(b)-n+i] != c {
			return nil, errors.New("Padded data not valid")
		}
	}
	return b[:len(b)-n], nil
}

func DecryptEncPart(key []byte, pe types.EncryptedData, etype EType, usage uint32) ([]byte, error) {
	//Derive the key
	k, err := etype.DeriveKey(key, GetUsageKe(usage))
	if err != nil {
		return nil, fmt.Errorf("Error deriving key: %v", err)
	}
	// Strip off the checksum from the end
	b, err := etype.Decrypt(k, pe.Cipher[:len(pe.Cipher)-etype.GetHMACBitLength()/8])
	if err != nil {
		return nil, fmt.Errorf("Error decrypting: %v", err)
	}
	//Verify checksum
	if !etype.VerifyIntegrity(key, pe.Cipher, b, usage) {
		return nil, errors.New("Error decrypting encrypted part: integrity verification failed")
	}
	//Remove the confounder bytes
	b = b[etype.GetConfounderByteSize():]
	if err != nil {
		return nil, fmt.Errorf("Error decrypting encrypted part: %v", err)
	}
	return b, nil
}

func GetKeyFromPassword(passwd string, cn types.PrincipalName, realm string, etypeId int, pas types.PADataSequence) (types.EncryptionKey, EType, error) {
	var key types.EncryptionKey
	etype, err := GetEtype(etypeId)
	if err != nil {
		return key, etype, fmt.Errorf("Error getting encryption type: %v", err)
	}
	sk2p := etype.GetDefaultStringToKeyParams()
	var salt string
	var paID int
	for _, pa := range pas {
		switch pa.PADataType {
		case patype.PA_PW_SALT:
			if paID > pa.PADataType {
				continue
			}
			salt = string(pa.PADataValue)
		case patype.PA_ETYPE_INFO:
			if paID > pa.PADataType {
				continue
			}
			var et types.ETypeInfo
			err := et.Unmarshal(pa.PADataValue)
			if err != nil {
				return key, etype, fmt.Errorf("Error unmashalling PA Data to PA-ETYPE-INFO2: %v", err)
			}
			if etypeId != et[0].EType {
				etype, err = GetEtype(et[0].EType)
				if err != nil {
					return key, etype, fmt.Errorf("Error getting encryption type: %v", err)
				}
			}
			salt = string(et[0].Salt)
		case patype.PA_ETYPE_INFO2:
			if paID > pa.PADataType {
				continue
			}
			var et2 types.ETypeInfo2
			err := et2.Unmarshal(pa.PADataValue)
			if err != nil {
				return key, etype, fmt.Errorf("Error unmashalling PA Data to PA-ETYPE-INFO2: %v", err)
			}
			if etypeId != et2[0].EType {
				etype, err = GetEtype(et2[0].EType)
				if err != nil {
					return key, etype, fmt.Errorf("Error getting encryption type: %v", err)
				}
			}
			if len(et2[0].S2KParams) == 4 {
				sk2p = hex.EncodeToString(et2[0].S2KParams)
			}
			salt = et2[0].Salt
		}
	}
	if salt == "" {
		salt = cn.GetSalt(realm)
	}
	k, err := etype.StringToKey(passwd, salt, sk2p)
	if err != nil {
		return key, etype, fmt.Errorf("Error deriving key from string: %+v", err)
	}
	key = types.EncryptionKey{
		KeyType:  etypeId,
		KeyValue: k,
	}
	return key, etype, nil
}

func getHash(pt, key []byte, usage []byte, etype EType) ([]byte, error) {
	k, err := etype.DeriveKey(key, usage)
	if err != nil {
		return nil, fmt.Errorf("Unable to derive key for checksum: %v", err)
	}
	mac := hmac.New(etype.GetHash, k)
	p := make([]byte, len(pt))
	copy(p, pt)
	mac.Write(p)
	return mac.Sum(nil)[:etype.GetHMACBitLength()/8], nil
}

func GetChecksumHash(pt, key []byte, usage uint32, etype EType) ([]byte, error) {
	return getHash(pt, key, GetUsageKc(usage), etype)
}

func GetIntegrityHash(pt, key []byte, usage uint32, etype EType) ([]byte, error) {
	return getHash(pt, key, GetUsageKi(usage), etype)
}

func VerifyIntegrity(key, ct, pt []byte, usage uint32, etype EType) bool {
	//The ciphertext output is the concatenation of the output of the basic
	//encryption function E and a (possibly truncated) HMAC using the
	//specified hash function H, both applied to the plaintext with a
	//random confounder prefix and sufficient padding to bring it to a
	//multiple of the message block size.  When the HMAC is computed, the
	//key is used in the protocol key form.
	h := make([]byte, etype.GetHMACBitLength()/8)
	copy(h, ct[len(ct)-etype.GetHMACBitLength()/8:])
	expectedMAC, _ := GetIntegrityHash(pt, key, usage, etype)
	return hmac.Equal(h, expectedMAC)
}

func VerifyChecksum(key, chksum, msg []byte, usage uint32, etype EType) bool {
	//The ciphertext output is the concatenation of the output of the basic
	//encryption function E and a (possibly truncated) HMAC using the
	//specified hash function H, both applied to the plaintext with a
	//random confounder prefix and sufficient padding to bring it to a
	//multiple of the message block size.  When the HMAC is computed, the
	//key is used in the protocol key form.
	expectedMAC, _ := GetChecksumHash(msg, key, usage, etype)
	return hmac.Equal(chksum, expectedMAC)
}

/*
Key Usage Numbers
RFC 3961: The "well-known constant" used for the DK function is the key usage number, expressed as four octets in big-endian order, followed by one octet indicated below.
Kc = DK(base-key, usage | 0x99);
Ke = DK(base-key, usage | 0xAA);
Ki = DK(base-key, usage | 0x55);
*/

// un - usage number
func GetUsageKc(un uint32) []byte {
	return getUsage(un, 0x99)
}

// un - usage number
func GetUsageKe(un uint32) []byte {
	return getUsage(un, 0xAA)
}

// un - usage number
func GetUsageKi(un uint32) []byte {
	return getUsage(un, 0x55)
}

func getUsage(un uint32, o byte) []byte {
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, un)
	return append(buf.Bytes(), o)
}

// Pass a usage value of zero to use the key provided directly rather than deriving one
func GetEncryptedData(pt []byte, key types.EncryptionKey, usage int, kvno int) (types.EncryptedData, error) {
	var ed types.EncryptedData
	etype, err := GetEtype(key.KeyType)
	if err != nil {
		return ed, fmt.Errorf("Error getting etype: %v", err)
	}
	k := key.KeyValue
	if usage != 0 {
		k, err = etype.DeriveKey(key.KeyValue, GetUsageKe(uint32(usage)))
		if err != nil {
			return ed, fmt.Errorf("Error deriving key: %v", err)
		}
	}
	//confounder
	c := make([]byte, etype.GetConfounderByteSize())
	_, err = rand.Read(c)
	if err != nil {
		return ed, fmt.Errorf("Could not generate random confounder: %v", err)
	}
	pt = append(c, pt...)
	_, b, err := etype.Encrypt(k, pt)
	if err != nil {
		return ed, fmt.Errorf("Error encrypting data: %v", err)
	}
	ih, err := GetIntegrityHash(pt, key.KeyValue, uint32(usage), etype)
	b = append(b, ih...)
	ed = types.EncryptedData{
		EType:  key.KeyType,
		Cipher: b,
		KVNO:   kvno,
	}
	return ed, nil
}
