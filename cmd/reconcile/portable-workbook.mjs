import ExcelJS from 'exceljs';

const color = hex => ({ argb: 'FF' + hex.slice(1) });

function columnNumber(letters) {
  let number = 0;
  for (const letter of letters) number = number * 26 + letter.charCodeAt(0) - 64;
  return number;
}

class SheetRange {
  constructor(sheet, row, column, height, width) {
    Object.assign(this, { sheet, row, column, height, width });
  }

  forEachCell(visit) {
    for (let y = 0; y < this.height; y++) {
      for (let x = 0; x < this.width; x++) {
        visit(this.sheet.getCell(this.row + y, this.column + x), y, x);
      }
    }
  }

  set values(rows) {
    this.forEachCell((cell, y, x) => {
      cell.value = rows[y][x] ?? null;
    });
  }

  set formulas(rows) {
    this.forEachCell((cell, y, x) => {
      cell.value = { formula: rows[y][x].slice(1) };
    });
  }

  set format({ fill, font, rowHeight }) {
    this.forEachCell(cell => {
      if (fill) cell.fill = { type: 'pattern', pattern: 'solid', fgColor: color(fill) };
      if (font) cell.font = { ...font, color: color(font.color) };
    });
    if (rowHeight) {
      for (let row = this.row; row < this.row + this.height; row++) {
        this.sheet.getRow(row).height = rowHeight;
      }
    }
  }

  get format() {
    const range = this;
    return {
      set columnWidthPx(pixels) {
        range.sheet.getColumn(range.column).width = Math.max(1, (pixels - 5) / 7);
      },
      set numberFormat(value) {
        range.forEachCell(cell => { cell.numFmt = value; });
      },
    };
  }

  merge() {
    this.sheet.mergeCells(this.row, this.column, this.row, this.column + this.width - 1);
  }
}

function sheetAPI(sheet) {
  return {
    name: sheet.name,
    getCell(row, column) {
      return new SheetRange(sheet, row + 1, column + 1, 1, 1);
    },
    getRangeByIndexes(row, column, height, width) {
      return new SheetRange(sheet, row + 1, column + 1, height, width);
    },
    getRange(address) {
      const m = /^([A-Z]+)(\d+)(?::([A-Z]+)(\d+))?$/.exec(address);
      if (!m) throw new Error(`Unsupported workbook range ${address}`);
      const [firstColumn, firstRow, lastColumn, lastRow] = m.slice(1);
      const c = columnNumber(firstColumn);
      const r = Number(firstRow);
      return new SheetRange(sheet, r, c, Number(lastRow ?? firstRow) - r + 1,
        columnNumber(lastColumn ?? firstColumn) - c + 1);
    },
    freezePanes: {
      freezeRows(ySplit) {
        sheet.views = [{ state: 'frozen', ySplit, showGridLines: false }];
      },
    },
  };
}

export function createPortableWorkbook() {
  const file = new ExcelJS.Workbook();
  file.calcProperties.fullCalcOnLoad = true;
  return {
    worksheets: { add: name => sheetAPI(file.addWorksheet(name)) },
    save: path => file.xlsx.writeFile(path),
  };
}
