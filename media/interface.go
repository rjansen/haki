package media

import (
	"io"
)

//Media is an interface to defines a media encoder/decoder contract
type Media interface {
	//Marshal writes a json representation of the struct instance
	Marshal(io.Writer, interface{}) error
	//Unmarshal reads a json representation into the struct instance
	Unmarshal(io.Reader, interface{}) error
	//MarshalBytes writes a json representation of the struct instance
	MarshalBytes(interface{}) ([]byte, error)
	//UnmarshalBytes reads a json representation into the struct instance
	UnmarshalBytes([]byte, interface{}) error
}
