// Package extract contains functions to extract information from the COS event log.
package extract

import (
	"fmt"

	"github.com/GoogleCloudPlatform/confidential-space/server/coscel"
	"github.com/google/go-eventlog/cel"
	pb "github.com/google/go-tpm-tools/proto/attest"
)

// VerifiedCOSState returns the AttestedCosState from the given event log.
func VerifiedCOSState(eventLog cel.CEL, registerType uint8) (*pb.AttestedCosState, error) {
	cosState := &pb.AttestedCosState{}
	cosState.Container = &pb.ContainerState{}
	cosState.HealthMonitoring = &pb.HealthMonitoringState{}
	cosState.Container.Args = make([]string, 0)
	cosState.Container.EnvVars = make(map[string]string)
	cosState.Container.OverriddenEnvVars = make(map[string]string)

	seenSeparator := false
	for _, record := range eventLog.Records() {
		if uint8(record.IndexType) != registerType {
			return nil, fmt.Errorf("expect registerType: %d, but get %d in a CEL record", registerType, record.IndexType)
		}

		switch record.IndexType {
		case cel.PCRType:
			if record.Index != coscel.EventPCRIndex {
				return nil, fmt.Errorf("found unexpected PCR %d in COS CEL log", record.Index)
			}
		case cel.CCMRType:
			if record.Index != coscel.COSCCELMRIndex {
				return nil, fmt.Errorf("found unexpected CCELMR %d in COS CEL log", record.Index)
			}
		default:
			return nil, fmt.Errorf("unknown COS CEL log index type %d", record.IndexType)
		}

		// The Content.Type is not verified at this point, so we have to fail
		// if we see any events that we do not understand. This ensures that
		// we either verify the digest of event event in this PCR/RTMA, or we
		// fail to replay the event log.
		// TODO: See if we can fix this to have the Content Type be verified.
		cosTlv, err := coscel.ParseToCOSTLV(record.Content)
		if err != nil {
			return nil, err
		}

		// verify digests for the cos cel content
		if err := cel.VerifyDigests(cosTlv, record.Digests); err != nil {
			return nil, err
		}

		// TODO: Add support for post-separator container data
		if seenSeparator {
			return nil, fmt.Errorf("found COS Event Type %v after LaunchSeparator event", cosTlv.EventType)
		}

		switch cosTlv.EventType {
		case coscel.ImageRefType:
			if cosState.Container.GetImageReference() != "" {
				return nil, fmt.Errorf("found more than one ImageRef event")
			}
			cosState.Container.ImageReference = string(cosTlv.EventContent)

		case coscel.ImageDigestType:
			if cosState.Container.GetImageDigest() != "" {
				return nil, fmt.Errorf("found more than one ImageDigest event")
			}
			cosState.Container.ImageDigest = string(cosTlv.EventContent)

		case coscel.RestartPolicyType:
			restartPolicy, ok := pb.RestartPolicy_value[string(cosTlv.EventContent)]
			if !ok {
				return nil, fmt.Errorf("unknown restart policy in COS eventlog: %s", string(cosTlv.EventContent))
			}
			cosState.Container.RestartPolicy = pb.RestartPolicy(restartPolicy)

		case coscel.ImageIDType:
			if cosState.Container.GetImageId() != "" {
				return nil, fmt.Errorf("found more than one ImageId event")
			}
			cosState.Container.ImageId = string(cosTlv.EventContent)

		case coscel.EnvVarType:
			envName, envVal, err := coscel.ParseEnvVar(string(cosTlv.EventContent))
			if err != nil {
				return nil, err
			}
			cosState.Container.EnvVars[envName] = envVal

		case coscel.ArgType:
			cosState.Container.Args = append(cosState.Container.Args, string(cosTlv.EventContent))

		case coscel.OverrideArgType:
			cosState.Container.OverriddenArgs = append(cosState.Container.OverriddenArgs, string(cosTlv.EventContent))

		case coscel.OverrideEnvType:
			envName, envVal, err := coscel.ParseEnvVar(string(cosTlv.EventContent))
			if err != nil {
				return nil, err
			}
			cosState.Container.OverriddenEnvVars[envName] = envVal
		case coscel.LaunchSeparatorType:
			seenSeparator = true
		case coscel.MemoryMonitorType:
			enabled := false
			if len(cosTlv.EventContent) == 1 && cosTlv.EventContent[0] == uint8(1) {
				enabled = true
			}
			cosState.HealthMonitoring.MemoryEnabled = &enabled
		default:
			return nil, fmt.Errorf("found unknown COS Event Type %v", cosTlv.EventType)
		}

	}
	return cosState, nil
}
