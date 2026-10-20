/** A source line and its offset from the top of the viewport, independent of wrapping. */
export interface FilePosition {
  line: number;
  offset: number;
  left: number;
}
