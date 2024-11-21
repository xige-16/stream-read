package funcutil

import (
	"fmt"
	"github.com/golang/protobuf/descriptor"
	"github.com/golang/protobuf/proto"
	"go.uber.org/zap"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/milvus-io/milvus-proto/go-api/v2/commonpb"
	"github.com/milvus-io/milvus-proto/go-api/v2/milvuspb"
	"github.com/xige-16/storage-test/pkg/log"
)

func GetVersion(m proto.GeneratedMessage) (string, error) {
	md, _ := descriptor.MessageDescriptorProto(m)
	if md == nil {
		log.Error("MessageDescriptorProto result is nil")
		return "", fmt.Errorf("MessageDescriptorProto result is nil")
	}
	extObj, err := proto.GetExtension(md.Options, milvuspb.E_MilvusExtObj)
	if err != nil {
		log.Error("GetExtension fail", zap.Error(err))
		return "", err
	}
	version := extObj.(*milvuspb.MilvusExt).Version
	log.Debug("GetVersion success", zap.String("version", version))
	return version, nil
}

func GetPrivilegeExtObj(m proto.GeneratedMessage) (commonpb.PrivilegeExt, error) {
	_, md := descriptor.MessageDescriptorProto(m)
	if md == nil {
		log.Info("MessageDescriptorProto result is nil")
		return commonpb.PrivilegeExt{}, fmt.Errorf("MessageDescriptorProto result is nil")
	}

	extObj, err := proto.GetExtension(md.Options, commonpb.E_PrivilegeExtObj)
	if err != nil {
		log.Info("GetExtension fail", zap.Error(err))
		return commonpb.PrivilegeExt{}, err
	}
	privilegeExt := extObj.(*commonpb.PrivilegeExt)
	log.Debug("GetPrivilegeExtObj success", zap.String("resource_type", privilegeExt.ObjectType.String()), zap.String("resource_privilege", privilegeExt.ObjectPrivilege.String()))
	return commonpb.PrivilegeExt{
		ObjectType:       privilegeExt.ObjectType,
		ObjectPrivilege:  privilegeExt.ObjectPrivilege,
		ObjectNameIndex:  privilegeExt.ObjectNameIndex,
		ObjectNameIndexs: privilegeExt.ObjectNameIndexs,
	}, nil
}

// GetObjectNames get object names from the grpc message according to the field index. The field is an array.
func GetObjectNames(m proto.GeneratedMessage, index int32) []string {
	if index <= 0 {
		return []string{}
	}
	msg := proto.MessageReflect(proto.MessageV1(m))
	msgDesc := msg.Descriptor()
	value := msg.Get(msgDesc.Fields().ByNumber(protoreflect.FieldNumber(index)))
	names, ok := value.Interface().(protoreflect.List)
	if !ok {
		return []string{}
	}
	res := make([]string, names.Len())
	for i := 0; i < names.Len(); i++ {
		res[i] = names.Get(i).String()
	}
	return res
}
