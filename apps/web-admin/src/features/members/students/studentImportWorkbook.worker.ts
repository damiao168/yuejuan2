import readExcelFile from "read-excel-file/web-worker";
import { studentSheetToCSV } from "./studentImportSheet";

self.onmessage = async (event: MessageEvent<File>) => {
  try {
    const result = await readExcelFile(await event.data.arrayBuffer(), { parseNumber: (value: string) => value, trim: false });
    const sheets = result.map(sheet => ({ name: sheet.sheet, csv: studentSheetToCSV(sheet.data) }));
    self.postMessage({ sheets });
  } catch (error) {
    self.postMessage({ error: error instanceof Error && /最多|超过|重名|无法读取/.test(error.message) ? error.message : "Excel名单读取失败，请检查文件或另存为 .xlsx 后重试" });
  }
};
