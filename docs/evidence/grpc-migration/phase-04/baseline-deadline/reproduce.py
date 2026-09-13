import collections,json,os,subprocess,time
import grpc
from marketdata.v1 import market_data_pb2 as pb
from marketdata.v1 import market_data_pb2_grpc as rpc
server=subprocess.Popen([os.environ['BASE_FIXTURE']],stdout=subprocess.PIPE,text=True)
counts=collections.Counter()
failures=[]
try:
    address=server.stdout.readline().strip()
    for i in range(200):
        with grpc.insecure_channel(address) as channel:
            grpc.channel_ready_future(channel).result(timeout=5)
            start=time.monotonic()
            try:
                rpc.MarketDataServiceStub(channel).ListTickers(pb.ListTickersRequest(symbol='wait'),timeout=0.1)
                counts['unexpected_success']+=1
            except grpc.RpcError as error:
                counts[error.code().name]+=1
                if error.code()!=grpc.StatusCode.DEADLINE_EXCEEDED:
                    failures.append({'index':i,'status':error.code().name,'details':error.details(),'elapsed_seconds':time.monotonic()-start})
    print(json.dumps({'base_revision':os.environ['BASE_REVISION'],'samples':200,'grpc_version':grpc.__version__,'counts':dict(counts),'non_deadline_outcomes':failures},indent=2))
finally:
    server.terminate()
    server.wait(timeout=10)
