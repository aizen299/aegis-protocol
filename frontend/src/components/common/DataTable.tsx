"use client";

import { DataGrid, type DataGridProps, type GridColDef, type GridValidRowModel } from "@mui/x-data-grid";

// Material's data grid for the sortable, paginated tables, styled from the shared theme. Rows are not
// virtualised: these tables are short, and every row is then in the page for search and tests.
export function DataTable<R extends GridValidRowModel>({
  rows,
  columns,
  getRowId,
  pageSize = 10,
  ...rest
}: {
  rows: R[];
  columns: GridColDef<R>[];
  getRowId: (row: R) => string;
  pageSize?: number;
} & Partial<DataGridProps<R>>) {
  return (
    <div className="w-full overflow-x-auto">
      <DataGrid<R>
        rows={rows}
        columns={columns}
        getRowId={getRowId}
        disableVirtualization
        disableRowSelectionOnClick
        disableColumnMenu
        rowHeight={44}
        columnHeaderHeight={40}
        autoHeight
        hideFooter={rows.length <= pageSize}
        initialState={{ pagination: { paginationModel: { pageSize } } }}
        pageSizeOptions={[pageSize, 25, 50]}
        sx={{
          border: 0,
          fontSize: 13,
          backgroundColor: "transparent",
          "--DataGrid-containerBackground": "transparent",
          "& .MuiDataGrid-main, & .MuiDataGrid-columnHeader, & .MuiDataGrid-columnHeaders, & .MuiDataGrid-filler, & .MuiDataGrid-topContainer, & .MuiDataGrid-footerContainer": {
            backgroundColor: "transparent",
          },
          "& .MuiDataGrid-cell": { display: "flex", alignItems: "center", lineHeight: "normal" },
          "& .MuiDataGrid-columnHeaderTitle": {
            fontSize: 11,
            fontWeight: 500,
            letterSpacing: "0.06em",
            textTransform: "uppercase",
            color: "text.secondary",
          },
          "& .MuiDataGrid-cell:focus, & .MuiDataGrid-columnHeader:focus": { outline: "none" },
          "& .MuiDataGrid-row:hover": { backgroundColor: "action.hover" },
        }}
        {...rest}
      />
    </div>
  );
}
