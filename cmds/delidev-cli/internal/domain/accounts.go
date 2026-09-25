package domain

const MaxAPIKeyBytes = 8192

func ValidateAPIKey(key []byte, keyless bool) error {
	if keyless {
		if len(key) != 0 {
			return Fail(InvalidArgument, "Keyless connections cannot include an API key.", "Choose exactly one authentication input.")
		}
		return nil
	}
	if len(key) == 0 {
		return Fail(MissingInput, "An API key is required.", "Send the key through the dedicated secret input channel.")
	}
	if len(key) > MaxAPIKeyBytes {
		return Fail(InvalidArgument, "The API key exceeds its size limit.", "Provide an API key of at most 8192 bytes.")
	}
	for _, b := range key {
		if b < 0x21 || b > 0x7e {
			return Fail(InvalidArgument, "The API key contains unsupported characters.", "Provide the exact printable ASCII key without whitespace or control characters.")
		}
	}
	return nil
}
