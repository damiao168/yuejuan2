"""构建离线可打开的SVG图纸索引；预览由draw.io原生渲染，不代替可编辑XML。"""
import html
import json
from pathlib import Path

from defusedxml import ElementTree as safe_xml

BASE = Path(__file__).resolve().parents[1]

def main():
    report=json.loads((BASE/'evidence/native-render-validation.json').read_text(encoding='utf-8'))
    pages=report['results']
    options=''.join(f'<option value="p{i}">{html.escape(p["file"][:2]+" / "+str(p["index"])+" "+p["title"])}</option>' for i,p in enumerate(pages))
    sections=[]
    for i,p in enumerate(pages):
        svg=(BASE/p['preview']).read_text(encoding='utf-8')
        # 原生导出只指定微软雅黑；离线阅读机未安装该字体时使用系统无衬线字体。
        # 仅补齐HTML内嵌副本的字体栈，不改原生SVG的尺寸、图形或可编辑图纸。
        svg=svg.replace("font-family: 'Microsoft YaHei';", "font-family: 'Microsoft YaHei',sans-serif;")
        # 保留原生尺寸；大页默认按 100% 阅读，只有主动选择适应页面时才缩小。
        # 离线预览只需 SVG 图形和尺寸；拒绝 DTD 或实体声明，不把展开后的内容嵌入 HTML。
        root=safe_xml.fromstring(svg, forbid_dtd=True)
        width=float(root.attrib['width'].removesuffix('px'))
        height=float(root.attrib['height'].removesuffix('px'))
        if width<=0 or height<=0:
            raise ValueError(f'无效的原生SVG尺寸：{p["preview"]}')
        sections.append(f'<section id="p{i}" data-file="{p["file"]}" class="sheet"><h2>{html.escape(p["title"])}</h2><p class="source">{p["file"]} · 第{p["index"]}页 · <a href="../diagrams/{p["file"]}">原生可编辑图纸</a></p><div class="drawing" data-width="{width:g}" data-height="{height:g}">{svg}</div></section>')
    output='''<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>yuejuan 架构图纸预览</title><style>
    :root{color-scheme:light;font-family:"Microsoft YaHei",sans-serif;color:#25364b;background:#eef2f7}body{margin:0}header{padding:18px 28px;background:#fff;border-bottom:1px solid #d6deea;position:sticky;top:0;z-index:3}h1{font-size:20px;margin:0 0 12px}select,button{font:inherit;padding:8px;border:1px solid #c9d4e3;border-radius:6px;background:#fff}#picker{max-width:65vw}button{margin-left:8px;cursor:pointer}#zoom{margin-left:8px}main{padding:24px}.sheet{display:none;background:white;padding:16px;border-radius:8px;max-width:1800px;margin:0 auto}.sheet.active{display:block}.drawing{overflow:auto;max-width:100%;max-height:calc(100vh - 210px);min-height:120px}.drawing svg{width:auto;height:auto;max-width:none;display:block}.sheet h2{font-size:17px;margin:0 0 5px}.source{font-size:12px;color:#68768b;margin:0 0 14px}a{color:#375e9a}.contacts{display:grid;grid-template-columns:repeat(3,1fr);gap:14px;padding:18px}.contacts .sheet{display:block;min-width:0;width:auto;padding:12px;border:1px solid #d6deea}.contacts .sheet h2{font-size:14px}.contacts .source{font-size:10px}.contacts .drawing{overflow:visible;max-height:none;min-height:0}.contacts .drawing svg{width:100%;height:400px;max-width:100%;object-fit:contain}body.contact header{display:none}body.contact main{padding:0}.meta{font-size:12px;color:#68768b;margin-left:10px}
    </style><header><h1>yuejuan · 当前实现架构图纸</h1><select id="picker" aria-label="选择架构图纸">'''+options+'''</select><button id="prev">上一页</button><button id="next">下一页</button><select id="zoom" aria-label="图纸缩放"><option value="fit">适应页面</option><option value="1" selected>100%</option><option value="1.25">125%</option><option value="1.5">150%</option></select><span class="meta">原生 draw.io 导出 SVG</span></header><main>'''+''.join(sections)+'''</main><script>
    const sheets=[...document.querySelectorAll('.sheet')], picker=document.querySelector('#picker'), zoom=document.querySelector('#zoom');
    function applyZoom(){
        if(document.body.classList.contains('contact'))return;
        const drawing=sheets[picker.selectedIndex].querySelector('.drawing'), svg=drawing.querySelector('svg');
        const width=Number(drawing.dataset.width),height=Number(drawing.dataset.height);
        const scale=zoom.value==='fit'?Math.min(drawing.clientWidth/width,Number.parseFloat(getComputedStyle(drawing).maxHeight)/height):Number(zoom.value);
        svg.style.width=width*scale+'px';svg.style.height=height*scale+'px';
    }
    // 深链接的锚点跳转会把标题推到固定页头下面；切页和初次加载后回到顶部。
    function resetPageScroll(){if(!document.body.classList.contains('contact'))window.scrollTo(0,0);}
    function show(i,updateHash=true){i=Math.max(0,Math.min(sheets.length-1,i));sheets.forEach((s,j)=>s.classList.toggle('active',i===j));picker.selectedIndex=i;applyZoom();if(updateHash){history.replaceState(null,'','#'+sheets[i].id);}resetPageScroll();}
    window.addEventListener('hashchange',()=>{if(!document.body.classList.contains('contact')){const i=sheets.findIndex(s=>s.id===location.hash.slice(1));if(i>=0)show(i,false);}});
    picker.onchange=()=>show(picker.selectedIndex);document.querySelector('#prev').onclick=()=>show(picker.selectedIndex-1);document.querySelector('#next').onclick=()=>show(picker.selectedIndex+1);
    zoom.onchange=applyZoom;window.addEventListener('resize',()=>{if(zoom.value==='fit')applyZoom();});
    window.addEventListener('load',()=>requestAnimationFrame(resetPageScroll));
    const params=new URLSearchParams(location.search);if(params.has('contact')){document.body.classList.add('contact');document.querySelector('main').classList.add('contacts');const start=Number(params.get('start')||0),count=Number(params.get('count')||9);sheets.forEach((s,i)=>s.style.display=i>=start&&i<start+count?'block':'none');}else{const target=sheets.findIndex(s=>s.id===location.hash.slice(1));show(Math.max(0,target));}
    </script></html>'''
    (BASE/'previews/index.html').write_text(output,encoding='utf-8')
    print(json.dumps({'preview_pages':len(pages),'offline_viewer':'previews/index.html'}))

if __name__=='__main__':
    main()
