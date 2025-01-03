#!/usr/bin/env bash

# Licensed to the LF AI & Data foundation under one
# or more contributor license agreements. See the NOTICE file
# distributed with this work for additional information
# regarding copyright ownership. The ASF licenses this file
# to you under the Apache License, Version 2.0 (the
# "License"); you may not use this file except in compliance
# with the License. You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

SOURCE="${BASH_SOURCE[0]}"
while [ -h "$SOURCE" ]; do # resolve $SOURCE until the file is no longer a symlink
  DIR="$( cd -P "$( dirname "$SOURCE" )" && pwd )"
  SOURCE="$(readlink "$SOURCE")"
  [[ $SOURCE != /* ]] && SOURCE="$DIR/$SOURCE" # if $SOURCE was a relative symlink, we need to resolve it relative to the path where the symlink file was located
done
ROOT_DIR="$( cd -P "$( dirname "$SOURCE" )/.." && pwd )"

PROTO_DIR=$ROOT_DIR/internal/proto/
API_PROTO_DIR=$ROOT_DIR/cmake_build/thirdparty/milvus-proto/proto
PROTOC_BIN=$ROOT_DIR/cmake_build/bin/protoc

PROGRAM=$(basename "$0")
GOPATH=$(go env GOPATH)

if [ -z $GOPATH ]; then
    printf "Error: the environment variable GOPATH is not set, please set it before running %s\n" $PROGRAM > /dev/stderr
    exit 1
fi

export PATH=${GOPATH}/bin:$PATH

# official go code ship with the crate, so we need to generate it manually.
pushd ${PROTO_DIR}

mkdir -p etcdpb
mkdir -p indexcgopb

mkdir -p internalpb
mkdir -p rootcoordpb

mkdir -p segcorepb
mkdir -p proxypb

mkdir -p indexpb
mkdir -p datapb
mkdir -p querypb
mkdir -p planpb

mkdir -p $ROOT_DIR/cmd/tools/migration/legacy/legacypb

protoc_opt="${PROTOC_BIN} --proto_path=${API_PROTO_DIR} --proto_path=."

${protoc_opt} --go_out=plugins=grpc,paths=source_relative:./internalpb internal.proto || { echo 'generate internal.proto failed'; exit 1; }
${protoc_opt} --go_out=plugins=grpc,paths=source_relative:./proxypb proxy.proto|| { echo 'generate proxy.proto failed'; exit 1; }
${protoc_opt} --go_out=plugins=grpc,paths=source_relative:./indexpb index_coord.proto|| { echo 'generate index_coord.proto failed'; exit 1; }
${protoc_opt} --go_out=plugins=grpc,paths=source_relative:./datapb data_coord.proto|| { echo 'generate data_coord.proto failed'; exit 1; }
${protoc_opt} --go_out=plugins=grpc,paths=source_relative:./querypb query_coord.proto|| { echo 'generate query_coord.proto failed'; exit 1; }
${protoc_opt} --go_out=plugins=grpc,paths=source_relative:./planpb plan.proto|| { echo 'generate plan.proto failed'; exit 1; }
${protoc_opt} --go_out=plugins=grpc,paths=source_relative:./segcorepb segcore.proto|| { echo 'generate segcore.proto failed'; exit 1; }

popd


