/*
Copyright <holder> All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import "fmt"

// Business Error Code Definitions
// Range: 100000-999999
// 0 represents success

// IaaS Resource Related Error Codes (1xxxxx)
//
//go:generate stringer -type ErrCode -trimprefix Err -output error_codes_str.go
type ErrCode int

const (
	// common errors (1000xx)
	ErrUnknown               ErrCode = 100000
	ErrInsufficientResource  ErrCode = 100001
	ErrResourceNotFound      ErrCode = 100002
	ErrInvalidParameter      ErrCode = 100003
	ErrPermissionDenied      ErrCode = 100004
	ErrExecuteOnHyperFailed  ErrCode = 100005
	ErrOwnerNotFound         ErrCode = 100006
	ErrEncryptionFailed      ErrCode = 100008
	ErrJSONMarshalFailed     ErrCode = 100009
	ErrResourcesInOrg        ErrCode = 100010
	ErrInvalidCIDR           ErrCode = 100011
	ErrCIDRTooBig            ErrCode = 100012
	ErrOperationNotSupported ErrCode = 100013

	// database related errors (1001xx)
	ErrDatabaseError  ErrCode = 100100
	ErrSQLSyntaxError ErrCode = 100101

	// User, Organization, Member related errors (1002xx)
	ErrUserNotFound       ErrCode = 100200
	ErrUserCreationFailed ErrCode = 100201
	ErrUserUpdateFailed   ErrCode = 100202
	ErrUserDeleteFailed   ErrCode = 100203
	ErrOrgNotFound        ErrCode = 100204
	ErrOrgCreationFailed  ErrCode = 100205
	ErrOrgUpdateFailed    ErrCode = 100206
	ErrOrgDeleteFailed    ErrCode = 100207
	ErrNoRoleOnUser       ErrCode = 100208
	ErrPasswordHashFailed ErrCode = 100209
	ErrPasswordMismatch   ErrCode = 100210

	// Member related errors (1003xx)
	ErrMemberNotFound       ErrCode = 100300
	ErrMemberCreationFailed ErrCode = 100301
	ErrMemberUpdateFailed   ErrCode = 100302
	ErrMemberDeleteFailed   ErrCode = 100303

	// Instance related errors (111xxx)
	ErrInstanceNotFound           ErrCode = 111001
	ErrInstanceCreationFailed     ErrCode = 111002
	ErrInstanceUpdateFailed       ErrCode = 111003
	ErrInstanceDeleteFailed       ErrCode = 111004
	ErrInstanceInvalidState       ErrCode = 111005
	ErrInstanceInvalidConfig      ErrCode = 111007
	ErrInstancePowerActionFail    ErrCode = 111008
	ErrInstanceNoRouter           ErrCode = 111009
	ErrInstanceNoPrimaryInterface ErrCode = 111010
	ErrInvalidDomainFormat        ErrCode = 111011
	ErrConsoleCreateFailed        ErrCode = 111012
	ErrConsoleNotFound            ErrCode = 111013
	ErrInvalidConsoleToken        ErrCode = 111014
	ErrInvalidMetadata            ErrCode = 111015

	// Flavor related errors (1119xx)
	ErrFlavorNotFound     ErrCode = 111901
	ErrFlavorCreateFailed ErrCode = 111902
	ErrFlavorUpdateFailed ErrCode = 111903
	ErrFlavorDeleteFailed ErrCode = 111904
	ErrFlavorInUse        ErrCode = 111905
	ErrDiskTooSmall       ErrCode = 111906

	// migration related errors (1118xx)
	ErrMigrationNotFound     ErrCode = 111801
	ErrMigrationCreateFailed ErrCode = 111802
	ErrMigrationUpdateFailed ErrCode = 111803
	ErrMigrationDeleteFailed ErrCode = 111804
	ErrMigrationInProgress   ErrCode = 111805

	// Volume related errors (121xxx)
	ErrVolumeNotFound         ErrCode = 121001
	ErrVolumeCreationFailed   ErrCode = 121002
	ErrVolumeUpdateFailed     ErrCode = 121003
	ErrVolumeDeleteFailed     ErrCode = 121004
	ErrVolumeAttachFailed     ErrCode = 121005
	ErrVolumeDetachFailed     ErrCode = 121006
	ErrVolumeInvalidState     ErrCode = 121007
	ErrVolumeInvalidSize      ErrCode = 121008
	ErrBootVolumeNotFound     ErrCode = 121009
	ErrBootVolumeUpdateFailed ErrCode = 121010
	ErrBootVolumeDeleteFailed ErrCode = 121011
	ErrVolumeIsInUse          ErrCode = 121012
	ErrBootVolumeCannotDetach ErrCode = 121013
	ErrVolumeIsBusy           ErrCode = 121014
	ErrVolumeIsRestoring      ErrCode = 121015

	// Snapshot/Backup related errors (1251xx)
	ErrBackupNotFound                      ErrCode = 125100
	ErrBackupCreationFailed                ErrCode = 125101
	ErrBackupUpdateFailed                  ErrCode = 125102
	ErrBackupDeleteFailed                  ErrCode = 125103
	ErrBackupInUse                         ErrCode = 125104
	ErrCannotRestoreWhileInstanceIsRunning ErrCode = 125105
	ErrCannotRestoreFromBackup             ErrCode = 125106
	ErrBackupInvalidState                  ErrCode = 125107

	// Consistency Group related errors (1252xx)
	ErrCGNotFound                  ErrCode = 125200
	ErrCGCreationFailed            ErrCode = 125201
	ErrCGUpdateFailed              ErrCode = 125202
	ErrCGDeleteFailed              ErrCode = 125203
	ErrCGInvalidState              ErrCode = 125204
	ErrCGIsBusy                    ErrCode = 125205
	ErrCGSnapshotExists            ErrCode = 125206
	ErrCGVolumeNotInSamePool       ErrCode = 125207
	ErrCGVolumeIsBusy              ErrCode = 125208
	ErrCGVolumeInvalidState        ErrCode = 125209
	ErrCGSnapshotNotFound          ErrCode = 125210
	ErrCGSnapshotCreationFailed    ErrCode = 125211
	ErrCGSnapshotDeleteFailed      ErrCode = 125212
	ErrCGSnapshotRestoreFailed     ErrCode = 125213
	ErrCGSnapshotIsBusy            ErrCode = 125214
	ErrCGCannotModifyWithSnapshots ErrCode = 125215
	ErrCGInstanceNotShutoff        ErrCode = 125216 // instance must be shutoff before restoring CG snapshot
	ErrCGNoVolumes                 ErrCode = 125217 // consistency group has no volumes
	ErrCGVolumeAttachedNoInstance  ErrCode = 125218 // volume status is attached but has no instance ID
	ErrCGSnapshotCannotRestore     ErrCode = 125219 // snapshot cannot be restored (invalid state)

	// Network related errors (131xxx)
	// IP Address related errors (1310xx)
	ErrAddressNotFound     ErrCode = 131001
	ErrAddressUpdateFailed ErrCode = 131002
	ErrAddressDeleteFailed ErrCode = 131003
	ErrInsufficientAddress ErrCode = 131004
	ErrAddressCreateFailed ErrCode = 131005
	ErrAddressInUse        ErrCode = 131006

	// Subnet related errors (1311xx)
	ErrSubnetNotFound               ErrCode = 131101
	ErrSubnetCreateFailed           ErrCode = 131102
	ErrSubnetUpdateFailed           ErrCode = 131103
	ErrSubnetDeleteFailed           ErrCode = 131104
	ErrSubnetShouldBePublic         ErrCode = 131105
	ErrSubnetShouldBeSite           ErrCode = 131106
	ErrPublicSubnetNotFound         ErrCode = 131107
	ErrSiteSubnetUpdateFailed       ErrCode = 131108
	ErrSubnetsCrossVPCInOneInstance ErrCode = 131109
	ErrPublicSubnetCannotInVPC      ErrCode = 131110

	// Interface related errors (1312xx)
	ErrInterfaceNotFound             ErrCode = 131201
	ErrInterfaceCreateFailed         ErrCode = 131202
	ErrInterfaceUpdateFailed         ErrCode = 131203
	ErrNotAllowInterfaceInSiteSubnet ErrCode = 131204
	ErrInterfaceDeleteFailed         ErrCode = 131205
	ErrCannotDeletePrimaryInterface  ErrCode = 131206
	ErrTooManyInterfaces             ErrCode = 131207
	ErrInterfaceInvalidSubnet        ErrCode = 131208

	// Floating IP related errors (1312xx)
	ErrFIPNotFound               ErrCode = 131201
	ErrFIPCreateFailed           ErrCode = 131202
	ErrFIPUpdateFailed           ErrCode = 131203
	ErrDeleteNativeFIPFailed     ErrCode = 131204
	ErrUpdatePublicIPFailed      ErrCode = 131205
	ErrUpdateInstIDOfFIPFailed   ErrCode = 131206
	ErrUpdateSubnetIDOfFIPFailed ErrCode = 131207
	ErrFIPDeleteFailed           ErrCode = 131208
	ErrFIPInUse                  ErrCode = 131209
	ErrDummyFIPCreateFailed      ErrCode = 131210
	ErrUpdateGroupIDFailed       ErrCode = 131211
	ErrFIPListFailed             ErrCode = 131212

	// VPC/Router related errors (1313xx)
	ErrRouterNotFound              ErrCode = 131301
	ErrRouterCreateFailed          ErrCode = 131302
	ErrRouterUpdateFailed          ErrCode = 131303
	ErrRouterUpdateDefaultSGFailed ErrCode = 131304
	ErrRouterDeleteFailed          ErrCode = 131305
	ErrRouterInUse                 ErrCode = 131306
	ErrRouterHasFloatingIPs        ErrCode = 131307
	ErrRouterHasSubnets            ErrCode = 131308
	ErrRouterHasPortmaps           ErrCode = 131309

	// IP Group related errors (1314xx)
	ErrIpGroupNotFound     ErrCode = 131401
	ErrIpGroupCreateFailed ErrCode = 131402
	ErrIpGroupUpdateFailed ErrCode = 131403
	ErrIpGroupDeleteFailed ErrCode = 131404
	ErrIpGroupInUse        ErrCode = 131405

	// load balancer related errors (1315xx)
	ErrLoadBalancerNotFound     = 131501
	ErrLoadBalancerListFailed   = 131502
	ErrLoadBalancerCreateFailed = 131503
	ErrLoadBalancerUpdateFailed = 131504
	ErrLoadBalancerDeleteFailed = 131505
	ErrVrrpInstanceNotFound     = 131506
	ErrVrrpInstanceCreateFailed = 131507
	ErrVrrpInstanceUpdateFailed = 131508
	ErrVrrpInstanceDeleteFailed = 131509
	ErrListenerNotFound         = 131510
	ErrListenerListFailed       = 131511
	ErrListenerCreateFailed     = 131512
	ErrListenerUpdateFailed     = 131513
	ErrListenerDeleteFailed     = 131514
	ErrBackendNotFound          = 131515
	ErrBackendListFailed        = 131516
	ErrBackendCreateFailed      = 131517
	ErrBackendUpdateFailed      = 131518
	ErrBackendDeleteFailed      = 131519

	// Security related errors (141xxx)
	ErrSecurityGroupNotFound       ErrCode = 141001
	ErrSecurityGroupCreateFailed   ErrCode = 141002
	ErrSecurityGroupUpdateFailed   ErrCode = 141003
	ErrSecurityGroupDeleteFailed   ErrCode = 141004
	ErrAssociateSG2InterfaceFailed ErrCode = 141005
	ErrAtLeastOneSGRequired        ErrCode = 141006
	ErrCannotDeleteDefaultSG       ErrCode = 141007
	ErrSGHasInterfaces             ErrCode = 141008
	ErrSecurityRuleNotFound        ErrCode = 141009
	ErrSecurityRuleInvalid         ErrCode = 141010
	ErrSecurityRuleDeleteFailed    ErrCode = 141011
	ErrSecurityRuleCreateFailed    ErrCode = 141012
	ErrSecurityRuleUpdateFailed    ErrCode = 141013

	// Image related errors (151xxx)
	ErrImageNotFound            ErrCode = 151000
	ErrImageInUse               ErrCode = 151001
	ErrImageNoQA                ErrCode = 151002
	ErrImageCreateFailed        ErrCode = 151003
	ErrImageUpdateFailed        ErrCode = 151004
	ErrImageDeleteFailed        ErrCode = 151005
	ErrImageNotAvailable        ErrCode = 151006
	ErrImageStorageCreateFailed ErrCode = 151007
	ErrImageStorageDeleteFailed ErrCode = 151008
	ErrImageStorageUpdateFailed ErrCode = 151009
	ErrImageStorageNotFound     ErrCode = 151010
	ErrRescueImageNotFound      ErrCode = 151011

	// ssh key related errors (161xxx)
	ErrSSHKeyNotFound       ErrCode = 161001
	ErrSSHKeyCreateFailed   ErrCode = 161002
	ErrSSHKeyUpdateFailed   ErrCode = 161003
	ErrSSHKeyDeleteFailed   ErrCode = 161004
	ErrSSHKeyGenerateFailed ErrCode = 161005
	ErrSSHKeyInUse          ErrCode = 161006

	// hypervisor/zone related errors (171xxx)
	ErrNoQualifiedHypervisor  ErrCode = 171001
	ErrHypervisorNotFound     ErrCode = 171002
	ErrHypervisorUpdateFailed ErrCode = 171003
	ErrHypervisorDeleteFailed ErrCode = 171004
	ErrHypervisorInvalidState ErrCode = 171005
	ErrZoneNotFound           ErrCode = 171006
	ErrUnsetDefaultZoneFailed ErrCode = 171007
	ErrZoneCreationFailed     ErrCode = 171008
	ErrZoneUpdateFailed       ErrCode = 171009
	ErrZoneDeleteFailed       ErrCode = 171010
	ErrHypersInZone           ErrCode = 171011

	// task related errors (181xxx)
	ErrTaskNotFound ErrCode = 181001

	// dictionary related errors (1998xx)
	ErrDictionaryRecordsNotFound ErrCode = 199801
	ErrDictionaryCreateFailed    ErrCode = 199802
	ErrDictionaryUpdateFailed    ErrCode = 199803
	ErrDictionaryDeleteFailed    ErrCode = 199804
)

type CLError struct {
	Err     error
	Code    ErrCode
	Message string
}

func (e *CLError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("Code %d: %s (Details: %+v)", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("Code %d: %s", e.Code, e.Message)
}

func (e *CLError) Unwrap() error {
	return e.Err
}

func NewCLError(code ErrCode, message string, err error) *CLError {
	return &CLError{
		Code:    code,
		Message: message,
		Err:     err,
	}
}
