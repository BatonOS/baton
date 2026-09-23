// SPDX-License-Identifier: Apache-2.0

















package core



const ExecutionChain = "core"









const ContractVersion = "1.0.0"



const Unknown = "unknown"








var (
	revision    string
	sourceState string
)



type Build struct {

	Version string `json:"version"`

	Revision string `json:"revision"`






	SourceState string `json:"source_state"`
}







type Status struct {
	ExecutionChain  string `json:"execution_chain"`
	ContractVersion string `json:"contract_version"`
	Build           Build  `json:"build"`
}



func CurrentStatus(version string) Status {
	return Status{
		ExecutionChain:  ExecutionChain,
		ContractVersion: ContractVersion,
		Build: Build{
			Version:     orUnknown(version),
			Revision:    orUnknown(revision),
			SourceState: stateOf(revision, sourceState),
		},
	}
}

func orUnknown(s string) string {
	if s == "" {
		return Unknown
	}
	return s
}









func stateOf(revision, s string) string {
	if !isCommitID(revision) {
		return Unknown
	}
	switch s {
	case "clean", "dirty":
		return s
	}
	return Unknown
}


func isCommitID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
