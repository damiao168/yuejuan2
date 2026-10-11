// 浏览器截图只检查本次文档预览；不访问业务账号、API或学生数据。
// 这是 playwright-cli run-code 的函数输入，由 runner 注入 page 后调用，不能直接作为 Node 入口自调用。
async (page) => {
  const origin = 'http://127.0.0.1:8766';
  const report = await (await page.request.get(`${origin}/evidence/native-render-validation.json`)).json();
  await page.setViewportSize({width:1800,height:1500});
  const captures = [];
  for (let start=0; start<report.pages; start+=9) {
    const name=`architecture-contact-${String(start/9+1).padStart(2,'0')}.png`;
    await page.goto(`${origin}/previews/index.html?contact=1&start=${start}&count=9`);
    await page.evaluate(()=>document.fonts.ready);
    await page.screenshot({path:`D:/project/yuejuan/output/playwright/${name}`,fullPage:true});
    captures.push({name,start,count:Math.min(9,report.pages-start)});
  }
  for (const [file,id,name] of [
    ['01-feature-tree.drawio','feature-overview','feature-overview.png'],
    ['02-system-architecture.drawio','logical','system-overview.png'],
    ['04-database-er.drawio','global','database-overview.png'],
    ['07-ai-grading.drawio','panel_kernel','ai-panel.png'],
    ['10-security-deployment.drawio','deployment','deployment-overview.png'],
  ]) {
    const index=report.results.findIndex(p=>p.file===file&&p.id===id);
    if(index<0) continue;
    await page.goto(`${origin}/previews/index.html#p${index}`);
    await page.waitForFunction(index=>document.querySelector('#picker').selectedIndex===index,index);
    await page.evaluate(()=>document.fonts.ready);
    // QA全图截图按宽度铺开；阅读器本身仍默认100%并提供滚动，不能将其裁切区域误当完整页面。
    await page.evaluate(()=>{
      const drawing=document.querySelector('.sheet.active .drawing');
      if (!drawing) return;
      const svg=drawing.querySelector('svg'), scale=drawing.clientWidth/Number(drawing.dataset.width);
      drawing.style.maxHeight='none';drawing.style.overflow='visible';
      svg.style.width=Number(drawing.dataset.width)*scale+'px';
      svg.style.height=Number(drawing.dataset.height)*scale+'px';
    });
    await page.evaluate(()=>window.scrollTo(0,0));
    await page.screenshot({path:`D:/project/yuejuan/output/playwright/${name}`,fullPage:true});
    captures.push({name,file,id,index});
  }
  return {pages:report.pages,captures};
}
