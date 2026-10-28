// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import * as UTIF from "utif";
import { captureFileBytes } from "./capturePdfPreview";
import { captureTiffOrientation, classicTiffDirectories, countCaptureTiffPages, inspectCaptureTiff, previewCaptureTiff, reorderCaptureTiff } from "./captureTiffPreview";
import { captureTiffDirectories, readCaptureTiffTags } from "./captureTiffDirectories";

// 独立手写两页、单像素灰阶 TIFF：原页分别为黑、白，验证改序不重编码像素。
function fixture(little = true, compression = 1) {
  const bytes = new ArrayBuffer(238); const view = new DataView(bytes);
  view.setUint16(0, little ? 0x4949 : 0x4d4d); view.setUint16(2, 42, little); view.setUint32(4, 8, little);
  for (let page = 0; page < 2; page++) {
    const offset = 8 + page * 114; view.setUint16(offset, 9, little);
    const tags = [[256, 4, 1], [257, 4, 1], [258, 3, 8], [259, 3, compression], [262, 3, 1], [273, 4, 236 + page], [277, 3, 1], [278, 4, 1], [279, 4, 1]];
    tags.forEach(([tag, type, value], index) => { const entry = offset + 2 + index * 12; view.setUint16(entry, tag, little); view.setUint16(entry + 2, type, little); view.setUint32(entry + 4, 1, little); if (type === 3) view.setUint16(entry + 8, value, little); else view.setUint32(entry + 8, value, little); });
    view.setUint32(offset + 110, page === 0 ? 122 : 0, little);
  }
  view.setUint8(236, 0); view.setUint8(237, 255); return bytes;
}

// BigTIFF使用真正的64位条目数/像素偏移；另带inline有理数与外置文字，验证不能按classic布局误读。
function bigFixture(little: boolean) {
  const bytes = new ArrayBuffer(528), view = new DataView(bytes);
  view.setUint16(0, little ? 0x4949 : 0x4d4d);view.setUint16(2,43,little);view.setUint16(4,8,little);view.setBigUint64(8,16n,little);
  for(let page=0;page<2;page++) {
    const offset=page ? 256 : 16;view.setBigUint64(offset,11n,little);
    const tags=[[256,4,1],[257,4,1],[258,3,8],[259,3,1],[262,3,1],[273,16,496+page],[277,3,1],[278,4,1],[279,16,1],[270,2,512],[282,5,72]];
    tags.forEach(([tag,type,value],index)=>{
      const entry=offset+8+index*20;view.setUint16(entry,tag,little);view.setUint16(entry+2,type,little);view.setBigUint64(entry+4,tag===270?12n:1n,little);
      if(type===3)view.setUint16(entry+12,value,little);
      else if(type===4)view.setUint32(entry+12,value,little);
      else if(type===5){view.setUint32(entry+12,value,little);view.setUint32(entry+16,1,little);}
      else view.setBigUint64(entry+12,BigInt(value),little);
    });
    view.setBigUint64(offset+228,page===0?256n:0n,little);
  }
  view.setUint8(496,0);view.setUint8(497,255);new Uint8Array(bytes,512,12).set(new TextEncoder().encode("test sample\0"));
  return bytes;
}
afterEach(() => vi.restoreAllMocks());
describe("TIFF original-page ordering", () => {
  it.each([true, false])("reorders complete classic TIFF pages while retaining all non-pointer original bytes (little=%s)", async (little) => {
    const original = fixture(little); const file = new File([original], "two.tiff", { type: "image/tiff" });
    const result = await reorderCaptureTiff(file, [1, 0]); const bytes = await captureFileBytes(result);
    const pages = UTIF.decode(bytes); pages.forEach(page => UTIF.decodeImage(bytes, page));
    expect(Array.from(UTIF.toRGBA8(pages[0]))).toEqual([255, 255, 255, 255]);
    expect(Array.from(UTIF.toRGBA8(pages[1]))).toEqual([0, 0, 0, 255]);
    const edited = new Set([4, 5, 6, 7, 118, 119, 120, 121, 232, 233, 234, 235]);
    expect([...new Uint8Array(bytes)].filter((_, index) => !edited.has(index))).toEqual([...new Uint8Array(original)].filter((_, index) => !edited.has(index)));
    expect(classicTiffDirectories(bytes).directories.map(item => item.offset)).toEqual([122, 8]);
  });
  it("decodes each real TIFF page locally and stops between pages after cancellation", async () => {
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockImplementation(() => ({ createImageData: (w: number, h: number) => ({ data: new Uint8ClampedArray(w * h * 4) }), putImageData: vi.fn(), drawImage: vi.fn(), setTransform: vi.fn() }) as never);
    vi.spyOn(HTMLCanvasElement.prototype, "toDataURL").mockReturnValue("data:image/jpeg;base64,YQ==");
    const next = vi.fn(); const file = new File([fixture()], "two.tiff");
    await previewCaptureTiff(file, new AbortController().signal, next); expect(next).toHaveBeenCalledTimes(2); expect(next).toHaveBeenLastCalledWith(expect.any(String), 2);
    const abort = new AbortController(); await expect(previewCaptureTiff(file, abort.signal, () => abort.abort())).rejects.toThrow("取消");
  });
  it("rejects malformed chains, unsupported codecs, truncated pixels and incomplete permutations without altering originals", async () => {
    const big = fixture(); new DataView(big).setUint16(2, 43, true); expect(() => classicTiffDirectories(big)).toThrow("BigTIFF");
    const cycle = fixture(); new DataView(cycle).setUint32(232, 8, true); expect(() => classicTiffDirectories(cycle)).toThrow("页面链异常");
    await expect(reorderCaptureTiff(new File([fixture(true, 999)], "unknown.tiff"), [1, 0])).rejects.toThrow("编码");
    await expect(reorderCaptureTiff(new File([fixture().slice(0, 237)], "truncated.tiff"), [1, 0])).rejects.toThrow("不完整");
    const source = new File([fixture()], "two.tiff"); await expect(reorderCaptureTiff(source, [0, 0])).rejects.toThrow("页序不完整");
    expect(new Uint8Array(await captureFileBytes(source))[236]).toBe(0);
  });
  it.each([true, false])("只读取BigTIFF主页面数量，不改写64位目录（little=%s）", async little => {
    const bytes = new ArrayBuffer(104), view = new DataView(bytes);
    view.setUint16(0, little ? 0x4949 : 0x4d4d); view.setUint16(2, 43, little); view.setUint16(4, 8, little); view.setBigUint64(8, 16n, little);
    view.setBigUint64(16, 1n, little); view.setBigUint64(44, 60n, little); view.setBigUint64(60, 1n, little);
    expect(await countCaptureTiffPages(new File([bytes], "big.tiff"))).toBe(2);
    view.setBigUint64(88, 16n, little);
    await expect(countCaptureTiffPages(new File([bytes], "loop.tiff"))).rejects.toThrow("页面链异常");
  });
  it.each([true,false])("BigTIFF可真实解码并无损改序，所有非主链指针字节保持相同（little=%s）",async little=>{
    const original=bigFixture(little),chain=captureTiffDirectories(original);
    const tags=readCaptureTiffTags(original,chain);expect(tags[0].t270).toEqual(["test sample"]);expect(tags[0].t282).toEqual([72]);
    const pixels:number[][]=[];
    vi.spyOn(HTMLCanvasElement.prototype,"getContext").mockImplementation(()=>({createImageData:(w:number,h:number)=>({data:new Uint8ClampedArray(w*h*4)}),putImageData:(image:{data:Uint8ClampedArray})=>pixels.push([...image.data]),drawImage:vi.fn(),setTransform:vi.fn()}) as never);
    vi.spyOn(HTMLCanvasElement.prototype,"toDataURL").mockReturnValue("data:image/jpeg;base64,YQ==");
    const source=new File([original],"big.tiff",{type:"image/tiff"});
    expect(await inspectCaptureTiff(source)).toEqual({pageCount:2,previewUnavailable:""});
    const result=await reorderCaptureTiff(source,[1,0]),bytes=await captureFileBytes(result);
    await previewCaptureTiff(result,new AbortController().signal,vi.fn());
    expect(pixels).toEqual([[255,255,255,255],[0,0,0,255]]);
    expect(captureTiffDirectories(bytes).directories.map(item=>item.offset)).toEqual([256,16]);
    const pointerBytes=new Set([chain.first,...chain.directories.map(item=>item.next)].flatMap(start=>Array.from({length:8},(_,index)=>start+index)));
    expect([...new Uint8Array(bytes)].filter((_,index)=>!pointerBytes.has(index))).toEqual([...new Uint8Array(original)].filter((_,index)=>!pointerBytes.has(index)));
    expect(new Uint8Array(await captureFileBytes(source))).toEqual(new Uint8Array(original));
  });
  it("classic超过100页仍可计数，预览资源限制不冒充页数读取失败",async()=>{
    const bytes=new ArrayBuffer(8+101*18),view=new DataView(bytes);view.setUint16(0,0x4949);view.setUint16(2,42,true);view.setUint32(4,8,true);
    for(let page=0;page<101;page++){const at=8+page*18;view.setUint16(at,1,true);view.setUint16(at+2,256,true);view.setUint16(at+4,4,true);view.setUint32(at+6,1,true);view.setUint32(at+10,1,true);view.setUint32(at+14,page===100?0:at+18,true);}
    const source=new File([bytes],"101-pages.tiff");expect(await countCaptureTiffPages(source)).toBe(101);
    expect(await inspectCaptureTiff(source)).toEqual({pageCount:101,previewUnavailable:expect.stringContaining("100页")});
  });
  it("超大TIFF不先分配完整原件缓冲区，进入显式原件核对路径",async()=>{
    const source=new File(["synthetic"],"large.tiff"),read=vi.fn();
    Object.defineProperty(source,"size",{value:256*1024*1024+1});Object.defineProperty(source,"arrayBuffer",{value:read});
    await expect(inspectCaptureTiff(source)).rejects.toThrow("256MiB");expect(read).not.toHaveBeenCalled();
  });
  it("拒绝BigTIFF不精确偏移、字段乘积越界、外置越界以及像素/元数据与指针重叠",async()=>{
    const unsafe=bigFixture(true);new DataView(unsafe).setBigUint64(8,9007199254740992n,true);expect(()=>captureTiffDirectories(unsafe)).toThrow("精确");
    const overflow=bigFixture(true);new DataView(overflow).setBigUint64(16+8+4,9007199254740991n,true);expect(()=>readCaptureTiffTags(overflow,captureTiffDirectories(overflow))).toThrow("长度");
    const outside=bigFixture(true);new DataView(outside).setBigUint64(16+8+9*20+12,520n,true);expect(()=>readCaptureTiffTags(outside,captureTiffDirectories(outside))).toThrow("越界");
    const aliased=fixture();new DataView(aliased).setUint32(8+2+5*12+8,118,true);await expect(reorderCaptureTiff(new File([aliased],"aliased.tiff"),[1,0])).rejects.toThrow("重叠");
    const metadata=bigFixture(true);new DataView(metadata).setBigUint64(16+8+9*20+12,244n,true);expect(()=>readCaptureTiffTags(metadata,captureTiffDirectories(metadata))).toThrow("重叠");
  });
  it.each([1, 2, 3, 4, 5, 6, 7, 8])("TIFF方向%s的四角落在显示范围，宽高交换与原标签一致", orientation => {
    const result = captureTiffOrientation(3, 5, orientation), [a,b,c,d,e,f] = result.transform;
    const points = [[0,0],[3,0],[0,5],[3,5]].map(([x,y]) => [a*x+c*y+e,b*x+d*y+f]);
    expect(new Set(points.map(point => point.join(","))).size).toBe(4);
    expect(Math.min(...points.map(point=>point[0]))).toBe(0); expect(Math.max(...points.map(point=>point[0]))).toBe(result.width);
    expect(Math.min(...points.map(point=>point[1]))).toBe(0); expect(Math.max(...points.map(point=>point[1]))).toBe(result.height);
  });
});
